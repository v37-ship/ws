package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"spa-booking/internal/config"
	"spa-booking/internal/db"
	"spa-booking/internal/email"
	"spa-booking/internal/http/handlers"
	"spa-booking/internal/http/middleware"
	"spa-booking/internal/payment"
	"spa-booking/internal/repo"
	"spa-booking/internal/service"
	"spa-booking/internal/whatsapp"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := config.Load()
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}

	ctx := context.Background()
	if err := db.Migrate(cfg.DatabaseURL); err != nil {
		log.Error("migrate", "err", err)
		os.Exit(1)
	}
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("db", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	users := repo.NewUserRepo(pool)
	sessions := repo.NewSessionRepo(pool)
	catalogRepo := repo.NewCatalogRepo(pool)
	bookings := repo.NewBookingRepo(pool)
	payments := repo.NewPaymentRepo(pool)
	paySettingsRepo := repo.NewPaymentSettingsRepo(pool)
	promos := repo.NewPromoRepo(pool)
	distances := repo.NewDistanceRepo(pool)
	careers := repo.NewCareerRepo(pool)
	testimonials := repo.NewTestimonialRepo(pool)
	foods := repo.NewFoodRepo(pool)
	heroes := repo.NewHeroRepo(pool)
	gallery := repo.NewGalleryRepo(pool)
	waOutbox := repo.NewWaRepo(pool)
	waSettingsRepo := repo.NewWaSettingsRepo(pool)

	waSettingsSvc := service.NewWaSettingsService(waSettingsRepo, service.WaConfig{
		CustomerEnabled: cfg.WACustomer,
		ReminderEnabled: true,
		RemindHour:      cfg.WAReminderHour,
		ReviewEnabled:   cfg.GoogleReviewURL != "",
		ReviewURL:       cfg.GoogleReviewURL,
		GroupMode:       cfg.WAGroupBooking,
		StaffGroupJID:   cfg.WAStaffGroup,
		DigestEnabled:   true,
		DigestHour:      cfg.WADigestHour,
		OutboundEnabled: true,
	}, log)

	authSvc := service.NewAuthService(users, sessions, cfg.SessionTTL)
	catalogSvc := service.NewCatalogService(catalogRepo)
	availSvc := service.NewAvailabilityService(catalogRepo)
	mailer := email.NewAuthenticatedSender(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPFrom, cfg.SMTPUser, cfg.SMTPPass)
	midtrans := payment.NewClient(cfg.MidtransServer, cfg.MidtransIsProd)
	waSvc := service.NewWaService(waOutbox, whatsapp.NewClient(cfg.WAAPIURL, cfg.WAAPIKey, cfg.WAInstance), log, cfg.WAEnabled, cfg.WASendRate, waSettingsSvc)
	waSvc.RecoverSending(ctx)
	bookSvc := service.NewBookingService(bookings, payments, catalogRepo, users, promos, distances, availSvc, midtrans, mailer, cfg.BookingHoldTTL, cfg.BaseURL, log,
		service.BookingOptions{
			PaymentMode:  cfg.PaymentMode,
			Wa:           waSvc,
			CustomerWA:   cfg.WACustomer,
			StaffGroup:   cfg.WAStaffGroup,
			GroupBooking: cfg.WAGroupBooking,
			RemindHour:   cfg.WAReminderHour,
			ReviewURL:    cfg.GoogleReviewURL,
			Settings:     waSettingsSvc,
		})
	paySettingsSvc := service.NewPaymentSettingsService(paySettingsRepo)
	reportSvc := service.NewReportService(repo.NewReportRepo(pool), bookings, payments)
	careerSvc := service.NewCareerService(careers)
	tmSvc := service.NewTestimonialService(testimonials)
	foodSvc := service.NewFoodService(foods)
	heroSvc := service.NewHeroService(heroes)
	gallerySvc := service.NewGalleryService(gallery)
	promoSvc := service.NewPromoService(promos)
	distanceSvc := service.NewDistanceService(distances)
	digestSvc := service.NewDigestService(bookSvc, waSvc, waSettingsSvc, cfg.WAStaffGroup, cfg.BaseURL, cfg.WADigestHour, log)
	if cfg.WAEnabled {
		log.Info("whatsapp outbound enabled", "instance", cfg.WAInstance, "api", cfg.WAAPIURL, "customer_messages", cfg.WACustomer)
	} else {
		log.Info("whatsapp outbound in mock mode (WA_ENABLED=false)")
	}

	if err := os.MkdirAll(cfg.UploadDir, 0o755); err != nil {
		log.Error("upload dir", "err", err)
		os.Exit(1)
	}

	if err := authSvc.EnsureAdmin(ctx, cfg.AdminEmail, cfg.AdminPassword, "Wangga Admin"); err != nil {
		log.Error("ensure admin", "err", err)
		os.Exit(1)
	}
	if err := catalogSvc.SeedDemo(ctx); err != nil {
		log.Error("seed", "err", err)
		os.Exit(1)
	}
	if err := tmSvc.SeedTestimonials(ctx); err != nil {
		log.Error("seed testimonials", "err", err)
		os.Exit(1)
	}
	if err := foodSvc.SeedFood(ctx); err != nil {
		log.Error("seed food", "err", err)
		os.Exit(1)
	}
	if err := distanceSvc.SeedDistanceZones(ctx); err != nil {
		log.Error("seed distance zones", "err", err)
		os.Exit(1)
	}

	srvHandlers, err := handlers.New(cfg, log, authSvc, catalogSvc, availSvc, bookSvc, reportSvc, careerSvc, tmSvc, foodSvc, heroSvc, gallerySvc, promoSvc, distanceSvc, paySettingsSvc, waSettingsSvc, waSvc, waOutbox, digestSvc)
	if err != nil {
		log.Error("handlers", "err", err)
		os.Exit(1)
	}

	r := chi.NewRouter()
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	r.Use(chimw.Timeout(60 * time.Second))
	r.Use(middleware.LoadUser(authSvc))
	srvHandlers.Routes(r)

	go func() {
		t := time.NewTicker(1 * time.Minute)
		defer t.Stop()
		for range t.C {
			if err := bookSvc.ExpireHolds(context.Background()); err != nil {
				log.Warn("expire holds", "err", err)
			}
			if digestSvc.Due(context.Background(), time.Now()) {
				if _, err := digestSvc.Run(context.Background(), false); err != nil {
					log.Warn("staff digest", "err", err)
				}
			}
			_ = sessions.DeleteExpired(context.Background())
		}
	}()

	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for range t.C {
			waSvc.Drain(context.Background())
		}
	}()

	httpSrv := &http.Server{Addr: cfg.HTTPAddr, Handler: r}
	go func() {
		log.Info("listening", "addr", cfg.HTTPAddr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
}
