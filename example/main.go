package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gin-gonic/gin"
	"github.com/luraproject/lura/config"
	"github.com/luraproject/lura/logging"
	"github.com/luraproject/lura/proxy"
	"github.com/luraproject/lura/router"
	krakendgin "github.com/luraproject/lura/router/gin"
	"github.com/luraproject/lura/transport/http/client"

	opencensus "github.com/scriptdash/krakend-opencensus"
	"github.com/scriptdash/krakend-opencensus/exporter"
	_ "github.com/scriptdash/krakend-opencensus/exporter/influxdb"
	_ "github.com/scriptdash/krakend-opencensus/exporter/jaeger"
	_ "github.com/scriptdash/krakend-opencensus/exporter/prometheus"
	_ "github.com/scriptdash/krakend-opencensus/exporter/zipkin"
	opencensusgin "github.com/scriptdash/krakend-opencensus/router/gin"
)

func main() {
	port := flag.Int("p", 0, "Port of the service")
	logLevel := flag.String("l", "ERROR", "Logging level")
	debug := flag.Bool("d", false, "Enable the debug")
	configFile := flag.String("c", "/etc/krakend/configuration.json", "Path to the configuration filename")
	flag.Parse()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		select {
		case sig := <-sigs:
			log.Println("Signal intercepted:", sig)
			cancel()
		case <-ctx.Done():
		}
	}()

	parser := config.NewParser()
	serviceConfig, err := parser.Parse(*configFile)
	if err != nil {
		log.Fatal("ERROR:", err.Error())
	}
	serviceConfig.Debug = serviceConfig.Debug || *debug
	if *port != 0 {
		serviceConfig.Port = *port
	}

	logger, _ := logging.NewLogger(*logLevel, os.Stdout, "[KRAKEND]")

	// Register stats and trace exporters to export the collected data.
	{
		exporter.Register(logger)

		if err := opencensus.Register(ctx, serviceConfig); err != nil {
			log.Fatal(err)
		}
	}

	bf := func(cfg *config.Backend) proxy.Proxy {
		return proxy.NewHTTPProxyWithHTTPExecutor(cfg, opencensus.HTTPRequestExecutorFromConfig(client.NewHTTPClient, cfg), cfg.Decoder)
	}

	// setup the krakend router
	routerFactory := krakendgin.NewFactory(krakendgin.Config{
		Engine:         gin.Default(),
		ProxyFactory:   opencensus.ProxyFactory(proxy.NewDefaultFactory(opencensus.BackendFactory(bf), logger)),
		Middlewares:    []gin.HandlerFunc{},
		Logger:         logger,
		HandlerFactory: opencensusgin.New(krakendgin.EndpointHandler),
		RunServer:      router.RunServer,
	})

	// start the engine
	routerFactory.NewWithContext(ctx).Run(serviceConfig)
}
