package main

import (
	"context"
	"flag"

	//	"log"
	"net"
	"strconv"
	"strings"

	replpb "github.com/acid_kvstore/proto/package/replicamgrpb"
	pbt "github.com/acid_kvstore/proto/package/txmanagerpb"
	"github.com/acid_kvstore/tx/txmanager"
	log "github.com/pingcap-incubator/tinykv/log"

	//log "github.com/sirupsen/logrus"
	"go.etcd.io/etcd/raft/raftpb"
	"google.golang.org/grpc"
)

func formatFilePath(path string) string {
	arr := strings.Split(path, "/")
	return arr[len(arr)-1]
}

// XXX:Setup properly
func setLogger(level string) {
	log.SetLevelByString(level)

	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)

	/*
		logrus.SetReportCaller(true)
		formatter := &logrus.TextFormatter{
			TimestampFormat:        "02-01-2006 15:04:05", // the "time" field configuratiom
			FullTimestamp:          true,
			DisableLevelTruncation: true, // log level field configuration
			CallerPrettyfier: func(f *runtime.Frame) (string, string) {
				// this function is required when you want to introduce your custom format.
				// In my case I wanted file and line to look like this `file="engine.go:141`
				// but f.File provides a full path along with the file name.
				// So in `formatFilePath()` function I just trimmet everything before the file name
				// and added a line number in the end
				return "", fmt.Sprintf("%s:%d", formatFilePath(f.File), f.Line)
			},
		}
		logrus.SetFormatter(formatter)

		//log.SetFormatter(&log.TextFormatter{})
		log.SetOutput(os.Stdout)
		// Only log the warning severity or above.
		//log.SetLevel(log.InfoLevel)
		// log.SetLevel(log.WarnLevel)
	*/
}

const WorkerThreads = 10

func main() {

	cluster := flag.String("cluster", "http://127.0.0.1:9021", "comma separated cluster peers")
	loglevel := flag.String("loglevel", "info", "info, warn, debug, error fatal")
	id := flag.Int("id", 1, "node ID")
	cliport := flag.Int("cliport", 9121, "http port")
	join := flag.Bool("join", false, "join an existing cluster")
	//kvcluster := flag.String("kvcluster", "http://127.0.0.1:9021", "comma separated KvServer cluster peers")
	grpcport := flag.String("grpcport", "127.0.0.1:9122", "grpc server port")

	replicamgrs := flag.String("replicamgrs", "127.0.0.1:9021", "comma separated replicamgrs")
	flag.Parse()
	//log.SetFlags(log.LstdFlags | log.Lshortfile)
	//log.SetReportCaller(true)
	setLogger(*loglevel)
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
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)

	// start worker threads
	for k := 0; k < WorkerThreads; k++ {
		go ts.TxCommitWorker()
		go ts.TxAbortWorker()
	}

	ip := strings.Split(*grpcport, ":")[0]
	go ts.LogCompactionTimer(ctx)

	ts.HttpEndpoint = "http://" + ip + ":" + strconv.Itoa(*cliport)
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
		log.Infof("error in leader update: %v", err)
	} else {
		ts.ShardInfo = resp.ShardInfo
	}

	for _, shard := range ts.ShardInfo.ShardMap {
		txmanager.TxKvCreateClientCtx(shard.LeaderKey)
	}
	// XXX: TxManager Server
	go func() {
		log.Infof("grpc port %s", *grpcport)
		port := ":" + strings.Split(*grpcport, ":")[1]
		lis, err := net.Listen("tcp", port)
		if err != nil {
			log.Fatalf("failed to listen: %v", err)
		}
		s := grpc.NewServer()
		pbt.RegisterTxmanagerServer(s, ts)
		if err := s.Serve(lis); err != nil {
			log.Fatalf("failed to serve: %v", err)
		}
	}()
	log.Infof("Starting setting up KvCLient")
	//compl := make(chan int)
	//go txmanager.NewTxKvManager(strings.Split(*kvcluster, ","), compl)

	//log.Infof("Waiting to get kvport client")
	//<-compl
	//XXX: Server HTTP api
	ts.ServeHttpTxApi(*cliport)
	//	go checkLeader(ctx, kvs)

	/* start the grpc server */

	cancel()
}
