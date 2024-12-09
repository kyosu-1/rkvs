# rkvs

This project provides a sample implementation of a distributed Key-Value Store using the HashiCorp Raft consensus protocol. It demonstrates a multi-node Raft cluster managed by Docker Compose, exposing an HTTP API for set/get operations. A frontend UI built with Remix, TypeScript, and Tailwind CSS allows you to interact with and visualize the cluster state more easily.

Features Overview
- Raft Cluster: A 5-node cluster (raft1 through raft5) where one node is the leader and the others are followers.
- KVS Operations: Use HTTP API endpoints (/set?key=...&value=..., /get?key=...) to store and retrieve arbitrary key-value pairs replicated across the cluster.
- Cluster Management: Add and remove nodes from the cluster using /join and /remove endpoints.
- Cluster Status: Check the current leader, cluster configuration, and stats with endpoints like /leader, /peers, and /stats.
UI: A separate Remix-based UI (run locally) to conveniently manage keys, view cluster status, and adjust the leader node address.

## development

### raft cluster

```
docker compose up -d
```

### ui

```
cd ui
npm install
npm run dev
```

## Usage

### Set a key-value pair

```
http://localhost:8081/set?key=foo&value=bar
```

### Get a key-value pair

```
http://localhost:8081/get?key=foo
```

### Check the Leader Node

``` 
curl http://localhost:8081/leader
```

## Changing the Leader Node

1. stop the current leader node

```
docker compose stop raft1
```

After a short delay, the remaining nodes detect leader absence and hold an election. A new leader emerges among raft2 ~ raft5.

