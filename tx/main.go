package main

import (
	"context"
	"flag"
	"net"
	"os"
	"strconv"
	"strings"

	replpb "github.com/acid_kvstore/proto/package/replicamgrpb"
	pbt "github.com/acid_kvstore/proto/package/txmanagerpb"
	"github.com/acid_kvstore/tx/txmanager"
	log "github.com/sirupsen/logrus"
	"go.etcd.io/etcd/raft/raftpb"
	"google.golang.org/grpc"
)

// XXX:Setup properly
func setLogger() {

	log.SetOutput(os.Stdout)
	// Only log the warning severity or above.
	log.SetLevel(log.InfoLevel)

}

func main() {

	cluster := flag.String("cluster", "http://127.0.0.1:9021", "comma separated cluster peers")
	id := flag.Int("id", 1, "node ID")
	cliport := flag.Int("cliport", 9121, "http port")
	join := flag.Bool("join", false, "join an existing cluster")
	//kvcluster := flag.String("kvcluster", "http://127.0.0.1:9021", "comma separated KvServer cluster peers")
	grpcport := flag.String("grpcport", "127.0.0.1:9122", "grpc server port")

	replicamgrs := flag.String("replicamgrs", "127.0.0.1:9021", "comma separated replicamgrs")
	flag.Parse()
	//log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.SetReportCaller(true)
	proposeC := make(chan string)
	defer close(proposeC)
	confChangeC := make(chan raftpb.ConfChange)
	defer close(confChangeC)

	//start the raft service
	var ts *txmanager.TxStore
	//	getSnapshot := func() ([]byte, error) { return ts.GetSnapshot() }
	//	tr = txmanager.NewTxRecord(cli)
	//	commitC, errorC, snapshotterReady, raft := raft.NewRaftNode(*id, strings.Split(*cluster, ","), *join, getSnapshot, proposeC, confChangeC)
	// 	ts = txmanager.NewTxStore(<-snapshotterReady, proposeC, commitC, errorC, raft)
	ts = txmanager.NewTxStoreWrapper(*id, strings.Split(*cluster, ","), *join)

	// start worker threads
	go ts.TxCommitWorker()
	go ts.TxAbortWorker()
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)

	ts.HttpEndpoint = "http://127.0.0.1:" + strconv.Itoa(*cliport)
	ts.RpcEndpoint = *grpcport

	//Create Connection to all servers
	for _, servers := range strings.Split(*replicamgrs, ",") {
		ts.StartReplicaServerConnection(context.Background(), servers)
	}
	// Keep Updating the replicaLeader
	//go QueryReplManagerForLeader(ctx)
	server := strings.Split(*replicamgrs, ",")
	//Update to ReplicaLeader
	ts.ReplLeaderClient = ts.ReplMgrs[server[0]]
	go ts.UpdateLeader(ctx)
	resp, err := ts.ReplLeaderClient.ReplicaQuery(context.Background(), &replpb.ReplicaQueryReq{})
	if err != nil {
		log.Printf("error in leader update: %v", err)
	} else {
		ts.ShardInfo = resp.ShardInfo
	}

	for _, shard := range ts.ShardInfo.ShardMap {
		txmanager.TxKvCreateClientCtx(shard.LeaderKey)
	}
	// XXX: TxManager Server
	go func() {
		lis, err := net.Listen("tcp", *grpcport)
		if err != nil {
			log.Fatalf("failed to listen: %v", err)
		}
		s := grpc.NewServer()
		pbt.RegisterTxmanagerServer(s, ts)
		if err := s.Serve(lis); err != nil {
			log.Fatalf("failed to serve: %v", err)
		}
	}()
	log.Printf("Starting setting up KvCLient")
	//compl := make(chan int)
	//go txmanager.NewTxKvManager(strings.Split(*kvcluster, ","), compl)

	//log.Printf("Waiting to get kvport client")
	//<-compl
	//XXX: Server HTTP api
	ts.ServeHttpTxApi(*cliport)
	//	go checkLeader(ctx, kvs)

	/* start the grpc server */

	cancel()
}
