package main

import (
	"context"
	"errors"
	"feedsystem_video_go/internal/auth"
	"feedsystem_video_go/internal/config"
	"feedsystem_video_go/internal/db"
	apphttp "feedsystem_video_go/internal/http"
	rabbitmq "feedsystem_video_go/internal/middleware/rabbitmq"
	rediscache "feedsystem_video_go/internal/middleware/redis"
	"feedsystem_video_go/internal/observability"
	"feedsystem_video_go/internal/upload"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	amqp "github.com/rabbitmq/amqp091-go"
)

func main() {
	// 加载 .env（本地开发）
	if err := godotenv.Load(); err != nil {
		log.Println(".env not found; continuing")
	}

	if err := auth.ValidateCookieOrigin(); err != nil {
		log.Fatal(err)
	}

	// 加载配置
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "configs/config.yaml"
	}
	log.Printf("Loading config from %s", configPath)
	cfg, usedDefault, err := config.LoadLocalDev(configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	if usedDefault {
		log.Printf("Config File %s not found, using default local config", configPath)
	} else {
		log.Printf("Config loaded from file: %s", configPath)
	}

	production := config.IsProduction()
	if production {
		if err := cfg.ValidateProduction(usedDefault, true); err != nil {
			log.Fatal(err)
		}
	}

	// 连接数据库
	//log.Printf("Database config: %v", cfg.Database)
	sqlDB, err := db.NewDB(cfg.Database)
	if err != nil {
		log.Fatalf("Failed to connect database: %v", err)
	}
	if err := db.AutoMigrate(sqlDB); err != nil {
		log.Fatalf("Failed to auto migrate database: %v", err)
	}
	defer db.CloseDB(sqlDB)

	// 连接 Redis (可选，用于缓存)
	cache, err := rediscache.NewFromEnv(&cfg.Redis)
	if err != nil {
		log.Printf("Redis config error (cache disabled): %v", err)
		cache = nil
	} else {
		pingCtx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		if err := cache.Ping(pingCtx); err != nil {
			log.Printf("Redis not available (cache disabled): %v", err)
			_ = cache.Close()
			cache = nil
		} else {
			defer cache.Close()
			log.Printf("Redis connected (cache enabled)")
		}
	}

	// 连接 RabbitMQ (可选，用于消息队列)
	rmq, err := rabbitmq.NewRabbitMQ(&cfg.RabbitMQ)
	if err != nil {
		log.Printf("RabbitMQ config error (disabled): %v", err)
		rmq = nil
	} else {
		defer rmq.Close()
		log.Printf("RabbitMQ connected")
	}

	if production && (cache == nil || rmq == nil) {
		log.Fatal("production API requires Redis and RabbitMQ")
	}

	// Pprof
	pprofServer, err := observability.NewPprofServer(
		"API",
		cfg.ObservabilityConfig.Pprof.Enabled,
		cfg.ObservabilityConfig.Pprof.ApiAddr,
	)
	if err != nil {
		log.Printf("Failed to start API pprof server: %v", err)
	}
	if pprofServer != nil {
		defer pprofServer.Close()
	}

	// 设置路由
	var uploads *upload.Service
	if enabled := os.Getenv("OSS_DIRECT_UPLOAD"); enabled != "" {
		direct, err := strconv.ParseBool(enabled)
		if err != nil {
			log.Fatal("invalid OSS_DIRECT_UPLOAD flag")
		}
		if direct {
			if cache == nil {
				log.Fatal("OSS direct upload requires Redis")
			}
			cloud, err := upload.NewAlibabaCloud(upload.CloudConfig{
				CDNAuthType: os.Getenv("CDN_AUTH_TYPE"), CDNAuthKey: os.Getenv("CDN_AUTH_KEY"),
				Region: os.Getenv("OSS_REGION"), Bucket: os.Getenv("OSS_BUCKET"),
				UploadRoleARN: os.Getenv("OSS_UPLOAD_ROLE_ARN"),
				AccessKeyID:   os.Getenv("OSS_ACCESS_KEY_ID"), AccessKeySecret: os.Getenv("OSS_ACCESS_KEY_SECRET"),
				UploadEndpoint: os.Getenv("OSS_UPLOAD_ENDPOINT"), PlaybackEndpoint: os.Getenv("OSS_PLAYBACK_ENDPOINT"), ProbePath: os.Getenv("OSS_FFPROBE_PATH"),
			})
			if err != nil {
				log.Fatalf("OSS direct upload configuration: %v", err)
			}
			uploads = upload.NewService(sqlDB, cloud)
			uploads.StartCleanup(context.Background())
		}
	}
	r := apphttp.SetRouter(sqlDB, cache, rmq, uploads)
	if production {
		r.RemoteIPHeaders = []string{"X-Forwarded-For"}
		if err := r.SetTrustedProxies([]string{os.Getenv("APP_TRUSTED_PROXY")}); err != nil {
			log.Fatal("invalid APP_TRUSTED_PROXY")
		}
		conn, err := sqlDB.DB()
		if err != nil {
			log.Fatal("could not obtain the MySQL pool")
		}
		r.GET("/readyz", func(c *gin.Context) {
			ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
			defer cancel()
			if rmq.Conn.IsClosed() || conn.PingContext(ctx) != nil || cache.Ping(ctx) != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"status": "ok"})
		})
		closed := rmq.Conn.NotifyClose(make(chan *amqp.Error, 1))
		go func() {
			if _, ok := <-closed; ok {
				log.Fatal("RabbitMQ connection lost; restarting API")
			}
		}()
	}

	address := ":" + strconv.Itoa(cfg.Server.Port)
	log.Printf("Server is running on port %d", cfg.Server.Port)
	if !production {
		if err := r.Run(address); err != nil {
			log.Fatalf("Failed to run server: %v", err)
		}
		return
	}

	server := &http.Server{
		Addr: address, Handler: r,
		ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 65 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	listener := make(chan error, 1)
	go func() { listener <- server.ListenAndServe() }()
	select {
	case err := <-listener:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP listener: %v", err)
		}
	case <-ctx.Done():
		deadline, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := server.Shutdown(deadline); err != nil {
			_ = server.Close()
			log.Println("HTTP shutdown deadline reached")
		}
	}
}
