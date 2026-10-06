// wrxbpq Web 服务入口（Vercel Go Framework Preset 识别根目录 main.go）。
//
// 前端静态资源通过 //go:embed 打进二进制，避免依赖部署机的目录布局；
// 监听 PORT 环境变量（Vercel 要求），本地默认 8080。
package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/wrxbpq/wrxbpq/internal/httpapi"
)

//go:embed all:public
var publicFiles embed.FS

func main() {
	mux := http.NewServeMux()
	// API 路由（/api/health、/api/probe、/api/generate、/api/verify）
	httpapi.RegisterAPI(mux)

	// public/ 静态资源挂到根路径；"/" 回落到 index.html。
	static, err := fs.Sub(publicFiles, "public")
	if err != nil {
		log.Fatal(err)
	}
	fileServer := http.FileServer(http.FS(static))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			if data, readErr := fs.ReadFile(static, "index.html"); readErr == nil {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write(data)
				return
			}
		}
		fileServer.ServeHTTP(w, r)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	server := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("wrxbpq listening on :%s", port)
	log.Fatal(server.ListenAndServe())
}
