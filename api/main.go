package main

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"os"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	fiberLogger "github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"
	"go.uber.org/zap"
	"tempmail/api/handlers"
	"tempmail/shared/db"
	"tempmail/shared/logger"
	"tempmail/shared/models"
	"time"
)

func main() {
	if err := logger.InitLogger("api"); err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer logger.Log.Sync()

	// Initialize databases
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		logger.Log.Fatal("DATABASE_URL environment variable is required")
	}

	if err := db.InitPostgres(dbURL); err != nil {
		logger.Log.Fatal("Failed to initialize PostgreSQL", zap.Error(err))
	}

	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		logger.Log.Fatal("REDIS_URL environment variable is required")
	}
	if err := db.InitRedis(redisURL); err != nil {
		logger.Log.Fatal("Failed to initialize Redis", zap.Error(err))
	}

	// AutoMigrate
	if err := models.Migrate(db.DB); err != nil {
		logger.Log.Fatal("Failed to migrate database", zap.Error(err))
	}

	app := fiber.New(fiber.Config{
		BodyLimit:             40 * 1024 * 1024,
		DisableStartupMessage: true,
		ServerHeader:          "",      // Don't leak server info
		AppName:               "",      // Don't leak app name
	})

	// -----------------------------------------------------------------------
	// GLOBAL MIDDLEWARES
	// -----------------------------------------------------------------------

	// Panic recovery
	app.Use(recover.New())

	// Request ID for audit trails
	app.Use(requestid.New())

	// Access logging
	app.Use(fiberLogger.New(fiberLogger.Config{
		Format:     "${time} | ${status} | ${latency} | ${ip} | ${method} ${path} | ${reqHeader:X-Request-Id}\n",
		TimeFormat: "2006-01-02 15:04:05",
	}))

	// Security headers
	app.Use(func(c *fiber.Ctx) error {
		c.Set("X-Content-Type-Options", "nosniff")
		c.Set("X-Frame-Options", "DENY")
		c.Set("X-XSS-Protection", "1; mode=block")
		c.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'self'; font-src 'self' https://fonts.gstatic.com https://fonts.googleapis.com; style-src-elem 'self' 'unsafe-inline' https://fonts.googleapis.com")
		c.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		return c.Next()
	})

	// CORS — optional, only enabled when FRONTEND_URL is set.
	frontendURL := os.Getenv("FRONTEND_URL")
	if frontendURL != "" {
		app.Use(cors.New(cors.Config{
			AllowOrigins:     frontendURL,
			AllowMethods:     "GET,POST,PUT,DELETE,OPTIONS",
			AllowHeaders:     "Origin,Content-Type,Accept,Authorization,X-API-Key,X-Request-Id",
			AllowCredentials: true,
			MaxAge:           3600,
		}))
		logger.Log.Info("CORS enabled", zap.String("origins", frontendURL))
	}

	// -----------------------------------------------------------------------
	// PUBLIC ROUTES (rate limited per IP — open to the world)
	// -----------------------------------------------------------------------
	publicLimiter := limiter.New(limiter.Config{
		Max:        60,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c *fiber.Ctx) string {
			return c.IP()
		},
		LimitReached: func(c *fiber.Ctx) error {
			logger.Log.Warn("Rate limit exceeded", zap.String("ip", c.IP()))
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error": "Rate limit exceeded. Try again later.",
			})
		},
	})

	app.Get("/health", publicLimiter, func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	// -----------------------------------------------------------------------
	// INTERNAL ROUTES — protected by Bearer token (mail-edge → api)
	// -----------------------------------------------------------------------
	internal := app.Group("/internal")
	internal.Use(internalAuthMiddleware)
	internal.Post("/mail/ingest", handlers.HandleMailIngest)

	// -----------------------------------------------------------------------
	// EXTERNAL SDK ROUTES — protected by X-API-Key (main web app → api)
	// -----------------------------------------------------------------------
	v1 := app.Group("/v1")
	v1.Use(apiKeyAuthMiddleware)
	v1.Post("/mailbox/create", handlers.HandleCreateMailbox)
	v1.Get("/mailbox/:id/messages", handlers.HandleGetMessages)
	v1.Get("/message/:id", handlers.HandleGetMessage)
	v1.Delete("/mailbox/:id", handlers.HandleDeleteMailbox)
	v1.Get("/domains", handlers.HandleListDomains)

	// -----------------------------------------------------------------------
	// ADMIN ROUTES — stricter rate limit (brute-force protection on login)
	// -----------------------------------------------------------------------
	adminLimiter := limiter.New(limiter.Config{
		Max:        20,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c *fiber.Ctx) string {
			return c.IP()
		},
		LimitReached: func(c *fiber.Ctx) error {
			logger.Log.Warn("Admin rate limit exceeded", zap.String("ip", c.IP()))
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error": "Too many requests.",
			})
		},
	})

	admin := app.Group("/admin")
	admin.Use(adminLimiter)

	// Login route — public (no auth middleware), rate-limited only
	admin.Post("/login", handlers.HandleAdminLogin)

	// Protected admin routes — require Bearer session token
	admin.Use(adminAuthMiddleware)
	admin.Get("/dashboard", handlers.HandleAdminDashboard)
	admin.Get("/domains", handlers.HandleAdminDomains)
	admin.Get("/domains/dns-check", handlers.HandleDNSCheck)
	admin.Post("/domains", handlers.HandleAdminCreateDomain)
	admin.Delete("/domains/:id", handlers.HandleAdminDeleteDomain)
	admin.Get("/mailboxes", handlers.HandleAdminMailboxes)
	admin.Delete("/mailboxes/:id", handlers.HandleAdminDeleteMailbox)
	admin.Get("/messages", handlers.HandleAdminMessages)
	admin.Get("/audit-log", handlers.HandleAdminAuditLog)
	admin.Get("/settings", handlers.HandleAdminGetSettings)
	admin.Post("/settings", handlers.HandleAdminUpdateSettings)

	port := os.Getenv("PORT")
	if port == "" {
		port = "4000"
	}

	// Serve admin UI at a fixed path from .env
	adminPanelPath := os.Getenv("ADMIN_PANEL_PATH")
	if adminPanelPath == "" {
		logger.Log.Warn("ADMIN_PANEL_PATH not set — admin UI disabled. Set it in .env for a fixed, shareable path.")
	} else {
		app.Get("/"+adminPanelPath, func(c *fiber.Ctx) error {
			return c.SendFile("./admin-ui/index.html")
		})
		app.Get("/"+adminPanelPath+"/*", func(c *fiber.Ctx) error {
			file := c.Params("*")
			if file == "" {
				file = "index.html"
			}
			return c.SendFile("./admin-ui/" + file)
		})
		logger.Log.Info("Admin panel available",
			zap.String("url", "http://localhost:"+port+"/"+adminPanelPath+"/"),
		)
	}

	logger.Log.Info("Starting API server", zap.String("port", port))
	if err := app.Listen(":" + port); err != nil {
		logger.Log.Fatal("API server failed", zap.Error(err))
	}
}

// ---------------------------------------------------------------------------
// AUTH MIDDLEWARES
// ---------------------------------------------------------------------------

// internalAuthMiddleware validates Bearer token for service-to-service comms
func internalAuthMiddleware(c *fiber.Ctx) error {
	reqToken := c.Get("Authorization")
	expectedToken := os.Getenv("INTERNAL_API_TOKEN")

	if expectedToken == "" {
		logger.Log.Error("INTERNAL_API_TOKEN not configured — rejecting all internal requests")
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Server misconfigured",
		})
	}

	if reqToken != "Bearer "+expectedToken {
		logger.Log.Warn("Invalid internal token attempt", zap.String("ip", c.IP()))
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "Forbidden",
		})
	}

	return c.Next()
}

// apiKeyAuthMiddleware validates X-API-Key for external SDK calls from web app
func apiKeyAuthMiddleware(c *fiber.Ctx) error {
	apiKey := c.Get("X-API-Key")
	expectedKey := os.Getenv("EXTERNAL_API_KEY")

	if expectedKey == "" {
		key := generateSecureToken(32)
		logger.Log.Warn("EXTERNAL_API_KEY not set. Generated temporary key — set in .env for production",
			zap.String("generated_key", key))
		os.Setenv("EXTERNAL_API_KEY", key)
		expectedKey = key
	}

	if apiKey != expectedKey {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "Invalid API key",
		})
	}

	return c.Next()
}

// adminAuthMiddleware validates session token (Bearer) for admin panel access
func adminAuthMiddleware(c *fiber.Ctx) error {
	secret := os.Getenv("ADMIN_API_KEY")
	if secret == "" {
		logger.Log.Error("ADMIN_API_KEY not configured — rejecting admin requests")
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "Admin panel not configured",
		})
	}

	// Check Authorization: Bearer <token>
	auth := c.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		token := strings.TrimPrefix(auth, "Bearer ")
		if username, ok := handlers.ValidateSessionToken(token, secret); ok {
			c.Locals("admin_user", username)
			return c.Next()
		}
	}

	return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
		"error": "Invalid or expired session",
	})
}

func generateSecureToken(length int) string {
	b := make([]byte, length)
	rand.Read(b)
	return hex.EncodeToString(b)
}
