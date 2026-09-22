package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"modbus-scan/internal/appconfig"
	"modbus-scan/internal/collector"
	"modbus-scan/internal/httpapi"
	"modbus-scan/internal/logging"
	"modbus-scan/internal/pointcsv"
	"modbus-scan/internal/realtime"
	devruntime "modbus-scan/internal/runtime"
	"modbus-scan/internal/service"
	"modbus-scan/internal/store"
	"modbus-scan/internal/winservice"
	webassets "modbus-scan/web"
)

func main() {
	if err := execute(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "modbus-scan: %v\n", err)
		os.Exit(1)
	}
}

func execute(args []string, stdout, _ io.Writer) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	executablePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	handled, err := dispatchCommand(ctx, args, executablePath, winservice.NewManager(), stdout)
	if handled || err != nil {
		return err
	}
	serviceMode, err := winservice.IsService()
	if err != nil {
		return err
	}
	if serviceMode {
		return winservice.Run(func(serviceCtx context.Context, ready func()) error {
			return runWithReady(serviceCtx, args, ready)
		})
	}
	return run(ctx, args)
}

func run(ctx context.Context, args []string) error {
	return runWithReady(ctx, args, func() {})
}

func runWithReady(ctx context.Context, args []string, ready func()) (retErr error) {
	flags := flag.NewFlagSet("modbus-scan", flag.ContinueOnError)
	configPath := flags.String("config", "configs/config.toml", "服务 TOML 配置文件路径")
	csvPath := flags.String("csv", "", "离线校验的点位 CSV 文件路径")
	validate := flags.Bool("validate", false, "仅验证 CSV，不启动服务")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}
	if *validate {
		if *csvPath == "" {
			return errors.New("validate mode requires -csv")
		}
		file, err := os.Open(*csvPath)
		if err != nil {
			return fmt.Errorf("open CSV: %w", err)
		}
		points, rowErrors, parseErr := pointcsv.Parse(file)
		closeErr := file.Close()
		if parseErr != nil {
			return fmt.Errorf("validate CSV: %w", parseErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close CSV: %w", closeErr)
		}
		if len(rowErrors) > 0 {
			details := make([]string, 0, len(rowErrors))
			for _, rowErr := range rowErrors {
				details = append(details, fmt.Sprintf("row %d %s: %s", rowErr.Row, rowErr.Field, rowErr.Message))
			}
			return fmt.Errorf("validate CSV: %s", strings.Join(details, "; "))
		}
		fmt.Printf("[OK] CSV 验证通过，共 %d 个点位\n", len(points))
		return nil
	}

	cfg, err := appconfig.Load(*configPath)
	if err != nil {
		return err
	}
	logger, logCloser, err := logging.New(cfg.Log)
	if err != nil {
		return err
	}
	defer logCloser.Close()
	defer func() {
		if retErr != nil {
			logger.Error("application stopped with error", "error", retErr)
		}
	}()

	database, err := store.Open(ctx, cfg.Database.Path, time.Duration(cfg.Database.BusyTimeoutMs)*time.Millisecond)
	if err != nil {
		return err
	}
	defer database.Close()

	realtimeHub := realtime.NewHub(500)
	manager := devruntime.NewManager(database, collector.NewRuntimeFactory(realtimeHub.Publish))
	for _, startErr := range manager.StartEnabled(ctx) {
		logger.Error("start configured device", "error", startErr)
	}
	devices := service.NewDeviceService(database, manager)
	points := service.NewPointService(database)
	router := httpapi.NewRouter(logger, devices, points, webassets.Assets, realtimeHub)
	server := httpapi.NewServer(cfg.Server, router)
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return fmt.Errorf("listen HTTP: %w", err)
	}

	serveErrors := make(chan error, 1)
	go func() {
		logger.Info("HTTP service started", "address", server.Addr)
		err := server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErrors <- err
			return
		}
		serveErrors <- nil
	}()
	ready()

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-serveErrors:
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Server.ShutdownTimeoutSec)*time.Second)
	defer cancel()
	httpErr := server.Shutdown(shutdownCtx)
	runtimeErr := manager.StopAll(shutdownCtx)
	if serveErr != nil {
		serveErr = fmt.Errorf("serve HTTP: %w", serveErr)
	}
	if httpErr != nil {
		httpErr = fmt.Errorf("shutdown HTTP: %w", httpErr)
	}
	if runtimeErr != nil {
		runtimeErr = fmt.Errorf("stop devices: %w", runtimeErr)
	}
	return errors.Join(serveErr, httpErr, runtimeErr)
}
