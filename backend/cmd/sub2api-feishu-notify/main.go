// Command sub2api-feishu-notify performs the explicit synthetic Feishu
// delivery smoke check. It never scans payment rows or changes payment state.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"time"

	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func main() {
	testNotification := flag.Bool("test-notification", false, "enqueue and wait for one synthetic Feishu payment notification")
	flag.Parse()
	if !*testNotification || flag.NArg() != 0 {
		flag.Usage()
		os.Exit(2)
	}

	cfg, err := config.LoadForBootstrap()
	if err != nil {
		log.Print("unable to load notification configuration")
		os.Exit(1)
	}
	if !cfg.FeishuPaymentAlerts.Enabled {
		log.Print("Feishu payment notifications are disabled")
		os.Exit(1)
	}
	client, err := repository.ProvideEnt(cfg)
	if err != nil {
		log.Print("unable to initialize notification store")
		os.Exit(1)
	}
	defer client.Close()
	db, err := repository.ProvideSQLDB(client)
	if err != nil {
		log.Print("unable to initialize notification store")
		os.Exit(1)
	}

	incidentService := service.NewFeishuPaymentIncidentService(
		repository.NewFeishuPaymentIncidentStore(db),
		service.NewFeishuPaymentIncidentSender(),
		cfg,
		nil,
		db,
	)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := incidentService.SendTestNotification(ctx); err != nil {
		// The service deliberately returns a generic provider error so this CLI
		// cannot print a webhook URL, response body, or Vault-agent detail.
		log.Print("Feishu payment test notification was not acknowledged")
		os.Exit(1)
	}
	log.Print("Feishu payment test notification acknowledged")
}
