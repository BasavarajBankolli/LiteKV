// Package rest exposes the LiteKV engine over a REST/JSON API using Gin.
//
// Endpoints:
//
//	GET    /v1/keys/:key          → get value
//	PUT    /v1/keys/:key          → set value (body: {"value": "<string or base64>"})
//	DELETE /v1/keys/:key          → delete key
//	POST   /v1/batch              → atomic batch write
//	GET    /v1/stats              → engine stats
//	GET    /healthz               → health check
package rest

import (
	"encoding/base64"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/BasavarajBankolli/litekv/internal/engine"
)

// Server is the REST API server.
type Server struct {
	eng    *engine.Engine
	router *gin.Engine
}

// New creates a REST server backed by the given engine.
func New(eng *engine.Engine) *Server {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(corsMiddleware()) // allow React dev server on :3000
	r.Use(requestLogger())

	s := &Server{eng: eng, router: r}
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	s.router.GET("/healthz", s.health)
	s.router.GET("/v1/stats", s.stats)

	v1 := s.router.Group("/v1/keys")
	v1.GET("/:key", s.get)
	v1.PUT("/:key", s.put)
	v1.DELETE("/:key", s.delete)

	s.router.POST("/v1/batch", s.batch)
}

// Run starts the HTTP server on addr (e.g. ":8080").
func (s *Server) Run(addr string) error {
	return s.router.Run(addr)
}

// Handler returns the underlying http.Handler for testing.
func (s *Server) Handler() http.Handler {
	return s.router
}

// --- Handlers ---

func (s *Server) health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) stats(c *gin.Context) {
	c.JSON(http.StatusOK, s.eng.Stats())
}

// GET /v1/keys/:key
func (s *Server) get(c *gin.Context) {
	key := c.Param("key")
	if key == "" {
		c.JSON(http.StatusBadRequest, errResponse("key is required"))
		return
	}
	val, err := s.eng.Get(key)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errResponse(err.Error()))
		return
	}
	if val == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "key not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"key":   key,
		"value": base64.StdEncoding.EncodeToString(val),
	})
}

// PUT /v1/keys/:key  body: {"value": "<string or base64>"}
func (s *Server) put(c *gin.Context) {
	key := c.Param("key")
	var req struct {
		Value string `json:"value" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errResponse(`body must be {"value": "..."}`))
		return
	}
	// Try base64 decode first; fall back to raw string
	valueBytes, err := base64.StdEncoding.DecodeString(req.Value)
	if err != nil {
		valueBytes = []byte(req.Value)
	}
	if err := s.eng.Put(key, valueBytes); err != nil {
		c.JSON(http.StatusInternalServerError, errResponse(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// DELETE /v1/keys/:key
func (s *Server) delete(c *gin.Context) {
	key := c.Param("key")
	if err := s.eng.Delete(key); err != nil {
		c.JSON(http.StatusInternalServerError, errResponse(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// POST /v1/batch
// Body: {"ops": [{"type": "put"|"delete", "key": "k", "value": "v"}, ...]}
func (s *Server) batch(c *gin.Context) {
	var req struct {
		Ops []struct {
			Type  string `json:"type"`
			Key   string `json:"key"`
			Value string `json:"value"`
		} `json:"ops"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errResponse(err.Error()))
		return
	}

	txn := s.eng.Begin()
	for _, op := range req.Ops {
		switch op.Type {
		case "put":
			val, err := base64.StdEncoding.DecodeString(op.Value)
			if err != nil {
				val = []byte(op.Value)
			}
			txn.Put(op.Key, val)
		case "delete":
			txn.Delete(op.Key)
		default:
			txn.Abort()
			c.JSON(http.StatusBadRequest, errResponse("unknown op type: "+op.Type))
			return
		}
	}
	if err := s.eng.Commit(txn); err != nil {
		txn.Abort()
		c.JSON(http.StatusInternalServerError, errResponse(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "ops_applied": len(req.Ops)})
}

// --- Middleware ---

// corsMiddleware allows the React dev server (localhost:3000) and any origin
// to call the API. Tighten this in production.
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, PUT, POST, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) { c.Next() }
}

func errResponse(msg string) gin.H {
	return gin.H{"error": msg}
}
