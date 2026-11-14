package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"go.uber.org/zap"

	"gvboard/internal/config"
)

// Server HTTP 服务，支持优雅关闭。
type Server struct {
	srv *http.Server
	log *zap.Logger
}

func New(cfg *config.Config, r *Router, log *zap.Logger) *Server {
	return &Server{
		srv: &http.Server{
			Addr:              cfg.Server.Listen,
			Handler:           r.Engine,
			ReadTimeout:       cfg.Server.ReadTimeout,
			ReadHeaderTimeout: 10 * time.Second,
			WriteTimeout:      cfg.Server.WriteTimeout,
			IdleTimeout:       cfg.Server.IdleTimeout,
			MaxHeaderBytes:    1 << 20,
		},
		log: log,
	}
}

// Run 启动服务并阻塞，直到 ctx 被取消后优雅关闭（最多等待 15 秒）。
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		s.log.Info("HTTP 服务已启动", zap.String("listen", s.srv.Addr))
		if err := s.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	s.log.Info("正在关闭 HTTP 服务")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return s.srv.Shutdown(shutdownCtx)
}
