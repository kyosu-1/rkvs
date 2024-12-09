package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/hashicorp/raft"
	boltdb "github.com/hashicorp/raft-boltdb"
	"github.com/rs/cors"
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

	parts := strings.Split(string(l.Data), " ")
	if len(parts) < 2 {
		return nil
	}

	cmd := parts[0]
	switch cmd {
	case "set":
		if len(parts) == 3 {
			key, val := parts[1], parts[2]
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
	data, err := io.ReadAll(rc)
	if err != nil {
		return err
	}
	newStore := make(map[string]string)
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		kv := strings.SplitN(line, "=", 2)
		if len(kv) == 2 {
			newStore[kv[0]] = kv[1]
		}
	}
	f.kvs = newStore
	return nil
}

type fsmSnapshot struct {
	store map[string]string
}

func (fs *fsmSnapshot) Persist(sink raft.SnapshotSink) error {
	var sb strings.Builder
	for k, v := range fs.store {
		sb.WriteString(k)
		sb.WriteString("=")
		sb.WriteString(v)
		sb.WriteString("\n")
	}
	if _, err := sink.Write([]byte(sb.String())); err != nil {
		sink.Cancel()
		return err
	}
	return sink.Close()
}

func (fs *fsmSnapshot) Release() {}

// ハンドラ関数群
func handleJoin(r *raft.Raft) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		id := q.Get("id")
		addr := q.Get("addr")
		if id == "" || addr == "" {
			http.Error(w, "id and addr required", http.StatusBadRequest)
			return
		}
		f := r.AddVoter(raft.ServerID(id), raft.ServerAddress(addr), 0, 0)
		if err := f.Error(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write([]byte("ok"))
	}
}

func handleSet(r *raft.Raft) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		key := q.Get("key")
		value := q.Get("value")
		if key == "" || value == "" {
			http.Error(w, "key and value required", http.StatusBadRequest)
			return
		}
		cmd := fmt.Sprintf("set %s %s", key, value)
		f := r.Apply([]byte(cmd), 5*time.Second)
		if f.Error() != nil {
			http.Error(w, f.Error().Error(), http.StatusInternalServerError)
			return
		}
		w.Write([]byte("ok"))
	}
}

func handleGet(fsm *fsm) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		key := q.Get("key")
		if key == "" {
			http.Error(w, "key required", http.StatusBadRequest)
			return
		}
		fsm.mu.Lock()
		val := fsm.kvs[key]
		fsm.mu.Unlock()
		w.Write([]byte(val))
	}
}

func handleLeader(r *raft.Raft) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		leader := r.Leader()
		w.Write([]byte(string(leader)))
	}
}

func handlePeers(r *raft.Raft) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		f := r.GetConfiguration()
		if err := f.Error(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var peers []map[string]string
		for _, srv := range f.Configuration().Servers {
			peer := map[string]string{
				"id":      string(srv.ID),
				"address": string(srv.Address),
				"voter":   strconv.FormatBool(srv.Suffrage == raft.Voter),
			}
			peers = append(peers, peer)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(peers)
	}
}

func handleStats(r *raft.Raft) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		stats := r.Stats()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stats)
	}
}

func handleRemove(r *raft.Raft) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		id := req.URL.Query().Get("id")
		if id == "" {
			http.Error(w, "id required", http.StatusBadRequest)
			return
		}
		f := r.RemoveServer(raft.ServerID(id), 0, 0)
		if err := f.Error(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write([]byte("removed"))
	}
}

func newHTTPRouter(r *raft.Raft, fsm *fsm) http.Handler {
	rtr := mux.NewRouter()
	rtr.HandleFunc("/join", handleJoin(r)).Methods("GET")
	rtr.HandleFunc("/set", handleSet(r)).Methods("GET")
	rtr.HandleFunc("/get", handleGet(fsm)).Methods("GET")
	rtr.HandleFunc("/leader", handleLeader(r)).Methods("GET")
	rtr.HandleFunc("/peers", handlePeers(r)).Methods("GET")
	rtr.HandleFunc("/stats", handleStats(r)).Methods("GET")
	rtr.HandleFunc("/remove", handleRemove(r)).Methods("GET")
	return rtr
}

func main() {
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
	} else if joinAddr != "" {
		// 自動JOIN
		go func() {
			for i := 0; i < 30; i++ {
				url := fmt.Sprintf("%s/join?id=%s&addr=%s", joinAddr, nodeID, raftAddr)
				resp, err := http.Get(url)
				if err == nil && resp.StatusCode == 200 {
					log.Printf("Joined cluster via %s", url)
					break
				}
				time.Sleep(2 * time.Second)
			}
		}()
	}

	handler := cors.AllowAll().Handler(newHTTPRouter(r, fsm))
	log.Printf("Starting HTTP server on %s", httpAddr)
	log.Fatal(http.ListenAndServe(httpAddr, handler))
}
