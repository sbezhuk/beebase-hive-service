// Command server is the entry point for the BeeBase hive-service.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"

	apphive "github.com/sbezhuk/beebase-hive-service/internal/application/hive"
	"github.com/sbezhuk/beebase-hive-service/internal/config"
	"github.com/sbezhuk/beebase-hive-service/internal/platform/apiaryclient"
	"github.com/sbezhuk/beebase-hive-service/internal/platform/harvestclient"
	"github.com/sbezhuk/beebase-hive-service/internal/platform/inspectionclient"
	"github.com/sbezhuk/beebase-hive-service/internal/platform/mediaclient"
	"github.com/sbezhuk/beebase-hive-service/internal/platform/notificationclient"
	"github.com/sbezhuk/beebase-hive-service/internal/platform/postgres"
	"github.com/sbezhuk/beebase-hive-service/internal/platform/subscriptionclient"
	repopostgres "github.com/sbezhuk/beebase-hive-service/internal/repository/postgres"
	transporthttp "github.com/sbezhuk/beebase-hive-service/internal/transport/http"
	hivehttp "github.com/sbezhuk/beebase-hive-service/internal/transport/http/hive"

	"github.com/sbezhuk/beebase-common/authmw"
	"github.com/sbezhuk/beebase-common/logger"
	"github.com/sbezhuk/beebase-common/server"
	"github.com/sbezhuk/beebase-common/sessionstore"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server exited with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	// .env is optional: present in local dev, absent in production/containers.
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log := logger.New(cfg.Env, cfg.LogLevel)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	connectCtx, cancelConnect := context.WithTimeout(ctx, cfg.DatabaseConnectTimeout)
	db, err := postgres.New(connectCtx, cfg.DatabaseURL)
	cancelConnect()
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer db.Close()

	log.Info("connected to database")

	redisConnectCtx, cancelRedisConnect := context.WithTimeout(ctx, cfg.RedisConnectTimeout)
	redisClient, err := sessionstore.NewRedisClient(redisConnectCtx, cfg.RedisAddr)
	cancelRedisConnect()
	if err != nil {
		return fmt.Errorf("connect to redis: %w", err)
	}
	defer redisClient.Close()

	log.Info("connected to redis")

	sessions := sessionstore.NewStore(redisClient)

	// Fails fast at boot if auth-service's JWKS endpoint isn't reachable,
	// consistent with how the database connection above is handled;
	// docker-compose orders auth-service before this service accordingly.
	verifier, err := authmw.NewVerifierFromJWKSURL(ctx, cfg.AuthJWKSURL, sessions)
	if err != nil {
		return fmt.Errorf("build JWKS verifier: %w", err)
	}

	hiveRepo := repopostgres.NewHiveRepository(db)
	apiaryVerifier := apiaryclient.New(cfg.ApiaryServiceURL)
	inspectionDeleter := inspectionclient.New(cfg.InspectionServiceURL)
	harvestDeleter := harvestclient.New(cfg.HarvestServiceURL)
	mediaDeleter := mediaclient.New(cfg.MediaServiceURL)
	subscriptionClient := subscriptionclient.New(cfg.SubscriptionServiceURL)
	notifications := notificationclient.New(cfg.NotificationServiceURL, cfg.InternalServiceToken)
	hiveService := apphive.NewService(hiveRepo, apiaryVerifier, inspectionDeleter, inspectionDeleter, mediaDeleter, subscriptionClient, harvestDeleter, notifications)
	hiveHandler := hivehttp.NewHandler(hiveService, log, cfg.PublicBaseURL, notifications)

	router := transporthttp.NewRouter(log, db, hiveHandler, verifier, cfg.InternalServiceToken)

	srv := server.New(server.Config{
		Addr:         ":" + cfg.HTTPPort,
		Handler:      router,
		ReadTimeout:  cfg.HTTPReadTimeout,
		WriteTimeout: cfg.HTTPWriteTimeout,
		IdleTimeout:  cfg.HTTPIdleTimeout,
	})

	errCh := make(chan error, 1)
	go func() {
		log.Info("starting http server", "port", cfg.HTTPPort, "env", cfg.Env)
		errCh <- srv.Run()
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("run server: %w", err)
		}
		return nil
	case <-ctx.Done():
		log.Info("shutdown signal received")
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), cfg.HTTPShutdownTimeout)
	defer cancelShutdown()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}

	log.Info("server stopped cleanly")
	return nil
}
