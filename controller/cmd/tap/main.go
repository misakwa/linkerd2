package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/linkerd/linkerd2/controller/k8s"
	"github.com/linkerd/linkerd2/controller/tap"
	"github.com/linkerd/linkerd2/pkg/admin"
	"github.com/linkerd/linkerd2/pkg/flags"
	log "github.com/sirupsen/logrus"
)

func main() {
	addr := flag.String("addr", ":8089", "address to serve on")
	metricsAddr := flag.String("metrics-addr", ":9998", "address to serve scrapable metrics on")
	kubeConfigPath := flag.String("kubeconfig", "", "path to kube config")
	controllerNamespace := flag.String("controller-namespace", "linkerd", "namespace in which Linkerd is installed")
	tapPort := flag.Uint("tap-port", 4190, "proxy tap port to connect to")
	flags.ConfigureAndParse()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	k8sAPI, err := k8s.InitializeAPI(
		*kubeConfigPath,
		k8s.DS,
		k8s.SS,
		k8s.Deploy,
		k8s.Job,
		k8s.NS,
		k8s.Pod,
		k8s.RC,
		k8s.Svc,
		k8s.RS,
	)
	if err != nil {
		log.Fatalf("Failed to initialize K8s API: %s", err)
	}

	tapClient, cc, err := tap.NewClient("127.0.0.1:8088")
	if err != nil {
		log.Fatal(err.Error())
	}

	defer cc.Close()

	server, lis, err := tap.NewHTTPSServer(*addr, tapClient, k8sAPI, *controllerNamespace, *kubeConfigPath)
	if err != nil {
		log.Fatal(err.Error())
	}

	grpcServer, grpcLis, err := tap.NewServer(":8088", *tapPort, *controllerNamespace, k8sAPI)
	if err != nil {
		log.Fatalf(err.Error())
	}

	k8sAPI.Sync() // blocks until caches are synced

	go func() {
		log.Println("starting gRPC server on", ":8088")
		grpcServer.Serve(grpcLis)
	}()

	go func() {
		log.Println("starting HTTPS server on", *addr)
		server.ServeTLS(lis, "", "")
	}()

	go admin.StartServer(*metricsAddr)

	<-stop

	log.Println("shutting down gRPC server on", *addr)
	grpcServer.GracefulStop()

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(1*time.Second))
	server.Shutdown(ctx)
	cancel()
}
