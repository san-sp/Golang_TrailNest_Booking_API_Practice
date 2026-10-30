package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
	"trailnest/internal/activity"
	"trailnest/internal/auth"
	"trailnest/internal/booking"
	"trailnest/internal/config"
	"trailnest/internal/database"
	"trailnest/internal/http/middleware"
	"trailnest/internal/payment"
	"trailnest/internal/reservation"
	"trailnest/internal/storage"
	"trailnest/internal/user"
	"trailnest/internal/wishlist"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if err := godotenv.Load(); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			slog.Info("no .env file found; using environment variables")
		} else {
			slog.Warn("could not load .env file; using environment variables", "error", err)
		}
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load configuration: %v", err)
	}

	startupCtx, cancelStartup := context.WithTimeout(context.Background(), 5*time.Second)

	pool, err := database.NewPool(startupCtx, cfg.DatabaseURL)
	cancelStartup()

	if err != nil {
		slog.Error("initialize database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	// Select Gin's behavior from validated configuration.
	gin.SetMode(cfg.GinMode)

	router := gin.New()

	if err := router.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		log.Fatalf("configure trusted proxies: %v", err)
	}

	router.Use(cors.New(cors.Config{
		AllowOrigins: []string{
			"http://localhost:8090",
		},
		AllowMethods: []string{
			http.MethodGet,
			http.MethodPost,
			http.MethodPatch,
			http.MethodDelete,
			http.MethodOptions,
		},
		AllowHeaders: []string{
			"Origin",
			"Content-Type",
			"Authorization",
			"Idempotency-Key",
		},
	}))

	router.Use(middleware.SecurityHeaders())

	if cfg.ImageStorageDriver == "local" {
		router.Static(
			cfg.ImageStoragePublicURL,
			cfg.ImageStorageDirectory,
		)
	}

	// Assign the request ID before logging and handling the request.
	router.Use(
		middleware.RequestID(),
		gin.Logger(),
		gin.Recovery(),
	)

	loginLimiter := middleware.NewRateLimiter(10, time.Minute)
	emailLimiter := middleware.NewRateLimiter(5, time.Minute)
	registerLimiter := middleware.NewRateLimiter(5, time.Minute)

	// Share the existing PostgreSQL pool with the user repository.
	userRepository := user.NewRepository(pool)

	tokenManager, err := auth.NewTokenManager(cfg.JWTSecret)
	if err != nil {
		slog.Error("initialize token manager", "error", err)
		os.Exit(1)
	}

	emailMailer, err := auth.NewEmailMailer(
		cfg.ResendAPIKey,
		cfg.EmailFrom,
		cfg.AppBaseURL,
	)
	if err != nil {
		slog.Error("initialize email mailer", "error", err)
		os.Exit(1)
	}

	registerHandler := auth.NewRegisterHandler(
		userRepository,
		emailMailer,
	)
	loginHandler := auth.NewLoginHandler(userRepository, tokenManager)

	meHandler := auth.NewMeHandler(userRepository)

	emailVerificationHandler := auth.NewEmailVerificationHandler(userRepository)
	resendVerificationHandler := auth.NewResendVerificationHandler(
		userRepository,
		emailMailer,
	)

	bookingRepository := booking.NewRepository(pool)
	bookingHandler := booking.NewHandler(bookingRepository)

	wishlistRepository := wishlist.NewRepository(pool)
	wishlistHandler := wishlist.NewHandler(wishlistRepository)

	activityRepository := activity.NewRepository(pool)

	var imageStorage activity.ImageStorage

	switch cfg.ImageStorageDriver {
	case "local":
		imageStorage, err = storage.NewLocalImageStorage(
			cfg.ImageStorageDirectory,
			cfg.ImageStoragePublicURL,
		)
		if err != nil {
			slog.Error("initialize local image storage", "error", err)
			os.Exit(1)
		}

		slog.Info("initialized local image storage")

	case "s3":
		awsCtx, cancelAWS := context.WithTimeout(
			context.Background(),
			10*time.Second,
		)

		awsCfg, awsErr := awsconfig.LoadDefaultConfig(
			awsCtx,
			awsconfig.WithRegion(cfg.AWSRegion),
		)

		cancelAWS()

		if awsErr != nil {
			slog.Error("load AWS configuration", "error", awsErr)
			os.Exit(1)
		}

		s3Client := s3.NewFromConfig(awsCfg)

		imageStorage, err = storage.NewS3ImageStorage(
			s3Client,
			cfg.S3Bucket,
			cfg.ImageStoragePublicURL,
		)
		if err != nil {
			slog.Error("initialize S3 image storage", "error", err)
			os.Exit(1)
		}

		slog.Info(
			"initialized S3 image storage",
			"bucket", cfg.S3Bucket,
			"region", cfg.AWSRegion,
		)

	default:
		slog.Error("unsupported image storage driver")
		os.Exit(1)
	}

	activityHandler := activity.NewHandlerWithImageStorage(
		activityRepository,
		imageStorage,
	)

	reservationRepository := reservation.NewRepository(pool)
	reservationHandler := reservation.NewHandler(reservationRepository)

	razorpayClient, err := payment.NewRazorpayClient(
		cfg.RazorpayKeyID,
		cfg.RazorpayKeySecret,
	)
	if err != nil {
		log.Fatalf("initialize Razorpay client: %v", err)
	}

	checkoutService := payment.NewCheckoutService(pool, razorpayClient)

	paymentRepository := payment.NewRepository(pool)
	paymentHandler := payment.NewHandler(
		checkoutService,
		paymentRepository,
	)
	webhookHandler := payment.NewWebhookHandler(
		paymentRepository,
		razorpayClient,
		cfg.RazorpayWebhookSecret,
	)

	// All routes in this group require a valid access token.
	protected := router.Group("/api/v1")
	protected.Use(auth.RequireAuthentication(tokenManager))
	protected.GET("/me", meHandler.Me)

	protected.POST("/bookings", bookingHandler.Create)
	protected.GET("/bookings", bookingHandler.ListMine)
	protected.GET("/bookings/:bookingID", bookingHandler.GetByID)
	protected.POST("/bookings/:bookingID/cancel", bookingHandler.Cancel)

	protected.POST("/wishlist", wishlistHandler.Add)
	protected.GET("/wishlist", wishlistHandler.List)
	protected.DELETE("/wishlist/:activityID", wishlistHandler.Remove)

	protected.POST("/reservations", reservationHandler.Create)
	protected.POST("/payments", paymentHandler.Create)
	protected.POST("/payments/verify", paymentHandler.Verify)

	// OPERATOR
	operator := router.Group("/api/v1/operator")
	operator.Use(
		auth.RequireAuthentication(tokenManager),
		auth.RequireRole("operator"),
	)

	operator.POST("/activities", activityHandler.OperatorCreate)
	operator.GET("/activities", activityHandler.OperatorList)
	operator.GET("/activities/:activityID", activityHandler.OperatorGetByID)
	operator.POST(
		"/activities/:activityID/image",
		activityHandler.OperatorUploadImage,
	)
	operator.PATCH("/activities/:activityID", activityHandler.OperatorUpdate)
	operator.PATCH(
		"/activities/:activityID/status",
		activityHandler.OperatorChangeStatus,
	)

	// ADMIN
	admin := router.Group("/api/v1/admin")
	admin.Use(
		auth.RequireAuthentication(tokenManager),
		auth.RequireRole("admin"),
	)
	adminUserHandler := auth.NewAdminUserHandler(userRepository)

	admin.POST("/activities", activityHandler.AdminCreate)
	admin.GET("/activities", activityHandler.AdminList)
	admin.GET("/activities/:activityID", activityHandler.AdminGetByID)
	admin.PATCH("/activities/:activityID", activityHandler.AdminUpdate)
	admin.PATCH("/activities/:activityID/status", activityHandler.AdminChangeStatus)
	admin.GET("/bookings", bookingHandler.AdminList)
	admin.GET("/bookings/:bookingID", bookingHandler.AdminGetByID)
	admin.GET("/reservations", reservationHandler.AdminList)
	admin.GET("/reservations/:reservationID", reservationHandler.AdminGetByID)
	admin.GET("/payments", paymentHandler.AdminList)
	admin.GET("/payments/:paymentID", paymentHandler.AdminGetByID)
	admin.GET("/users", adminUserHandler.List)
	admin.GET("/users/:userID", adminUserHandler.GetByID)
	admin.PATCH("/users/:userID/role", adminUserHandler.PromoteToOperator)
	admin.POST("/payments/:paymentID/reconcile", paymentHandler.AdminReconcileCapturedPayment)

	router.POST("/api/v1/auth/register", registerLimiter.Middleware(),
		middleware.MaxBodyBytes(1<<20),
		registerHandler.Register,
	)
	router.POST("/api/v1/auth/login", loginLimiter.Middleware(), loginHandler.Login)
	router.GET("/api/v1/auth/verify-email", emailVerificationHandler.VerifyEmail)
	router.POST("/api/v1/auth/resend-verification", emailLimiter.Middleware(),
		resendVerificationHandler.Resend,
	)

	router.GET("/api/v1/activities", activityHandler.List)
	router.GET("/api/v1/activities/:activityID", activityHandler.GetByID)

	router.POST("/api/v1/webhooks/razorpay", webhookHandler.Handle)

	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"service": "trailnest",
		})
	})

	router.GET("/readyz", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()

		if err := pool.Ping(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"status": "not_ready",
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"status": "ready",
		})
	})

	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	shutdownCtx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	// Run reservation expiry independently of incoming HTTP requests.
	// Wait for the worker to exit during graceful shutdown.
	var workerWG sync.WaitGroup

	workerWG.Add(1)
	go func() {
		defer workerWG.Done()

		reservation.RunExpiryWorker(
			shutdownCtx,
			reservationRepository,
			time.Minute,
			100,
		)
	}()

	refundWorker := payment.NewRefundWorker(
		paymentRepository,
		razorpayClient,
	)

	workerWG.Add(1)
	go func() {
		defer workerWG.Done()

		payment.RunRefundWorker(
			shutdownCtx,
			refundWorker,
			5*time.Second,
		)
	}()

	// Recover stale refund jobs without creating duplicate provider refunds.
	workerWG.Add(1)
	go func() {
		defer workerWG.Done()

		payment.RunRefundRecoveryWorker(
			shutdownCtx,
			refundWorker,
			5*time.Minute,
			30*time.Minute,
			20,
		)
	}()

	serverErrors := make(chan error, 1)

	go func() {
		slog.Info("TrailNest API starting", "port", cfg.Port)

		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
		close(serverErrors)
	}()

	select {
	case <-shutdownCtx.Done():
		slog.Info("shutdown signal received")
	case err := <-serverErrors:
		if err != nil {
			slog.Error("HTTP server failed", "error", err)
			os.Exit(1)
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		slog.Error("graceful shutdown failed", "error", err)
		if closerErr := server.Close(); closerErr != nil {
			slog.Error("force close server", "error", closerErr)
		}
		return
	}

	workerWG.Wait()

	slog.Info("TrailNest API stopped")
}
