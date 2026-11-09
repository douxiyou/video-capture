# Video Capture

## API 服务

需要 Go 1.24 或更高版本。使用 Cobra CLI 启动 Chi HTTP 服务：

```sh
go run . serve
```

默认监听 `:8080`。使用 `--addr` 指定监听地址，例如只监听本机：

```sh
go run . serve --addr 127.0.0.1:8080
```

健康检查：

```sh
curl http://127.0.0.1:8080/health
```

响应为 `{"status":"ok"}`。使用 Ctrl+C 或发送 SIGTERM 可以优雅停止服务。

运行测试：

```sh
go test ./...
```
