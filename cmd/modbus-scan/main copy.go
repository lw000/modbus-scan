package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"modbus-scan/internal/collector"
	"modbus-scan/internal/config"
	"modbus-scan/internal/udm"
)

func main() {
	// 命令行参数
	var (
		configFile string
		csvFile    string
		validate   bool
	)

	flag.StringVar(&configFile, "config", "config.toml", "TOML 配置文件路径")
	flag.StringVar(&csvFile, "csv", "points.csv", "点位 CSV 文件路径")
	flag.BoolVar(&validate, "validate", false, "仅验证 CSV 配置文件，不启动采集服务")
	flag.Parse()

	// 验证模式：仅加载并校验 CSV，输出结果后退出
	if validate {
		points, err := config.LoadPointsFromCSV(csvFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[FAIL] CSV 验证失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("[OK] CSV 验证通过，共 %d 个有效点位:\n", len(points))
		fmt.Printf("%-20s %-14s %8s %-10s %4s %4s %8s %8s %s\n",
			"TagName", "RegType", "Address", "DataType", "Bit", "Len", "Scale", "Offset", "Writeable")
		fmt.Println("--------------------------------------------------------------------------------")
		for _, p := range points {
			fmt.Printf("%-20s %-14s %8d %-10s %4d %4d %8.2f %8.2f %t\n",
				p.TagName, p.RegType, p.Address, p.DataType, p.BitOffset, p.BitLen, p.Scale, p.Offset, p.Writeable)
		}
		return
	}

	// 正常启动模式
	cfg, err := config.LoadDeviceConfig(configFile)
	if err != nil {
		log.Fatalf("加载配置文件失败 [%s]: %v", configFile, err)
	}

	connMgr := collector.NewConnManager(cfg)
	if err := connMgr.Connect(); err != nil {
		log.Fatalf("首次连接设备失败: %v", err)
	}

	points, err := config.LoadPointsFromCSV(csvFile)
	if err != nil {
		log.Fatalf("点位配置加载失败 [%s]: %v", csvFile, err)
	}

	chunks := config.OptimizeChunks(points)
	udmInstance := udm.New()
	c := collector.NewCollector(connMgr, udmInstance, chunks, cfg)

	scanInterval := time.Duration(cfg.Modbus.ScanIntervalMs) * time.Millisecond
	go c.ScanLoop(scanInterval)

	log.Printf("[INFO] Modbus 数据采集服务已启动 (周期: %v, 点位数: %d, 配置: %s, CSV: %s)",
		scanInterval, len(points), configFile, csvFile)

	// 优雅退出：监听系统信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	log.Printf("[INFO] 收到信号 %v，服务正在关闭...", sig)
}
