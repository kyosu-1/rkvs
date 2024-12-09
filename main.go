package main

import (
    "bytes"
    "fmt"
    "io"
    "log"
    "net/http"
    "os"
    "path/filepath"
    "sync"
    "time"

    "github.com/gorilla/mux"
    "github.com/hashicorp/raft"
    boltdb "github.com/hashicorp/raft-boltdb"
)

type fsm struct {
    mu  sync.Mutex
    kvs map[string]string
}

func newFSM() *fsm {
    return &fsm{kvs: make(map[string]string)}
}

func (f *fsm) Apply(l *raft.Log) interface{} {
    f.mu.Lock()
    defer f.mu.Unlock()

    parts := bytes.Split(l.Data, []byte(" "))
    if len(parts) < 2 {
        return nil
    }

    cmd := string(parts[0])
    switch cmd {
    case "set":
        if len(parts) == 3 {
            key := string(parts[1])
            val := string(parts[2])
            f.kvs[key] = val
            return val
        }
    }
    return nil
}

func (f *fsm) Snapshot() (raft.FSMSnapshot, error) {
    f.mu.Lock()
    defer f.mu.Unlock()

    clone := make(map[string]string, len(f.kvs))
    for k, v := range f.kvs {
        clone[k] = v
    }
    return &fsmSnapshot{store: clone}, nil
}

func (f *fsm) Restore(rc io.ReadCloser) error {
    f.mu.Lock()
    defer f.mu.Unlock()

    buf := new(bytes.Buffer)
    _, err := buf.ReadFrom(rc)
    if err != nil {
        return err
    }
    newStore := make(map[string]string)
    lines := bytes.Split(buf.Bytes(), []byte("\n"))
    for _, line := range lines {
        if len(line) == 0 {
            continue
        }
        kv := bytes.SplitN(line, []byte("="), 2)
        if len(kv) == 2 {
            newStore[string(kv[0])] = string(kv[1])
        }
    }
    f.kvs = newStore
    return nil
}

type fsmSnapshot struct {
    store map[string]string
}

func (fs *fsmSnapshot) Persist(sink raft.SnapshotSink) error {
    var buf bytes.Buffer
    for k, v := range fs.store {
        buf.WriteString(k)
        buf.WriteString("=")
        buf.WriteString(v)
        buf.WriteString("\n")
    }

    if _, err := sink.Write(buf.Bytes()); err != nil {
        sink.Cancel()
        return err
    }
    return sink.Close()
}

func (fs *fsmSnapshot) Release() {}

type RaftNode struct {
    raft *raft.Raft
    fsm  *fsm
}

func main() {
    // 環境変数でノード設定
    // NODE_ID: ノードID (必須)
    // RAFT_ADDR: "host:port" の形式でこのノードがリッスンするアドレス(必須)
    // BOOTSTRAP: "true"であればクラスタ初期化
    // JOIN_ADDR: "http://<leader-host>:8080" のように、LeaderへJoinリクエストを送る先(Leader以外は必須)
    // HTTP_ADDR: KV操作用HTTPサーバのアドレス(":8080"など)
    nodeID := os.Getenv("NODE_ID")
    raftAddr := os.Getenv("RAFT_ADDR")
    bootstrap := os.Getenv("BOOTSTRAP") == "true"
    joinAddr := os.Getenv("JOIN_ADDR")
    httpAddr := os.Getenv("HTTP_ADDR")
    if httpAddr == "" {
        httpAddr = ":8080"
    }

    if nodeID == "" || raftAddr == "" {
        log.Fatalf("NODE_ID and RAFT_ADDR must be set")
    }

    dataDir := "./data-" + nodeID
    os.MkdirAll(dataDir, 0755)

    config := raft.DefaultConfig()
    config.LocalID = raft.ServerID(nodeID)

    // BoltDBストアのセットアップ
    boltStorePath := filepath.Join(dataDir, "raft.db")
    boltStore, err := boltdb.NewBoltStore(boltStorePath)
    if err != nil {
        log.Fatalf("failed to create bolt store: %v", err)
    }

    stableStore := boltStore
    logStore := boltStore

    snapshotStorePath := filepath.Join(dataDir, "snapshots")
    os.MkdirAll(snapshotStorePath, 0755)
    snapshotStore, err := raft.NewFileSnapshotStore(snapshotStorePath, 1, os.Stderr)
    if err != nil {
        log.Fatalf("failed to create snapshot store: %v", err)
    }

    // トランスポート作成
    transport, err := raft.NewTCPTransport(raftAddr, nil, 3, 10*time.Second, os.Stderr)
    if err != nil {
        log.Fatalf("failed to create transport: %v", err)
    }

    fsm := newFSM()
    r, err := raft.NewRaft(config, fsm, logStore, stableStore, snapshotStore, transport)
    if err != nil {
        log.Fatalf("failed to create raft instance: %v", err)
    }

    if bootstrap {
        configuration := raft.Configuration{
            Servers: []raft.Server{
                {
                    ID:      raft.ServerID(nodeID),
                    Address: raft.ServerAddress(raftAddr),
                },
            },
        }
        r.BootstrapCluster(configuration)
        log.Println("Bootstrapped the cluster.")
    } else {
        // Leaderに対してJOINする必要がある場合は起動後にHTTP APIを通じてJOINする
        // ただしここでは手動で/joinエンドポイントにアクセスして処理することを想定
        // CLIで待つのもありですが、ここでは起動後にユーザがGETリクエストを行う想定
        log.Println("Waiting to join the cluster... Please call /join endpoint on leader.")
    }

    raftNode := &RaftNode{
        raft: r,
        fsm:  fsm,
    }

    rtr := mux.NewRouter()
    rtr.HandleFunc("/join", func(w http.ResponseWriter, req *http.Request) {
        // /join?id=node2&addr=192.168.0.11:7000 のような形式
        q := req.URL.Query()
        id := q.Get("id")
        addr := q.Get("addr")
        if id == "" || addr == "" {
            http.Error(w, "id and addr query params required", http.StatusBadRequest)
            return
        }

        f := r.AddVoter(raft.ServerID(id), raft.ServerAddress(addr), 0, 0)
        if err := f.Error(); err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }

        w.WriteHeader(http.StatusOK)
        w.Write([]byte("ok"))
    }).Methods("GET")

    rtr.HandleFunc("/set", func(w http.ResponseWriter, req *http.Request) {
        // /set?key=foo&value=bar
        q := req.URL.Query()
        key := q.Get("key")
        value := q.Get("value")
        if key == "" || value == "" {
            http.Error(w, "key and value query params required", http.StatusBadRequest)
            return
        }

        cmd := fmt.Sprintf("set %s %s", key, value)
        f := r.Apply([]byte(cmd), 5*time.Second)
        if f.Error() != nil {
            http.Error(w, f.Error().Error(), http.StatusInternalServerError)
            return
        }

        w.WriteHeader(http.StatusOK)
        w.Write([]byte("ok"))
    }).Methods("GET")

    rtr.HandleFunc("/get", func(w http.ResponseWriter, req *http.Request) {
        // /get?key=foo
        q := req.URL.Query()
        key := q.Get("key")
        if key == "" {
            http.Error(w, "key query param required", http.StatusBadRequest)
            return
        }

        raftNode.fsm.mu.Lock()
        val := raftNode.fsm.kvs[key]
        raftNode.fsm.mu.Unlock()

        w.WriteHeader(http.StatusOK)
        w.Write([]byte(val))
    }).Methods("GET")

    rtr.HandleFunc("/leader", func(w http.ResponseWriter, req *http.Request) {
        leader := r.Leader()
        w.WriteHeader(http.StatusOK)
        w.Write([]byte(string(leader)))
    }).Methods("GET")

    // HTTPサーバ起動
    log.Printf("Starting HTTP server on %s", httpAddr)
    go func() {
        if err := http.ListenAndServe(httpAddr, rtr); err != nil {
            log.Fatalf("http server error: %v", err)
        }
    }()

    if !bootstrap && joinAddr != "" {
        // 自動JOIN: Leaderの/joinエンドポイントにリクエスト
        // NOTE: Leaderが既に起動・ブートストラップされていることが前提
        leaderJoinURL := joinAddr + "/join?id=" + nodeID + "&addr=" + raftAddr
        for i := 0; i < 30; i++ {
            resp, err := http.Get(leaderJoinURL)
            if err == nil && resp.StatusCode == 200 {
                log.Printf("Successfully joined cluster via %s", leaderJoinURL)
                break
            }
            time.Sleep(2 * time.Second)
        }
    }

    select {}
}
