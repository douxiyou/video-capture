# Video Capture

## API 服务

需要 Go 1.24 或更高版本。首次执行 `go run`、`go build` 或 `go test` 前，请先按照下方说明构建前端。后端配置由 Viper 读取 YAML 文件，先复制示例并根据需要修改：

```sh
cp config.example.yaml config.yaml
```

默认读取当前工作目录的 `config.yaml`；文件不存在时使用内置默认值。使用 `--config` 指定其他配置文件时，文件必须存在。配置错误、未知字段或类型错误会使命令失败。配置中的相对路径相对于**当前工作目录**，不是配置文件所在目录。启动 Chi HTTP 服务：

```sh
go run . serve
# 或使用指定配置
go run . --config /path/to/config.yaml serve
```

服务监听地址在 `server.addr` 中设置。原有 `--addr`、`--db`、`--experimental-face-registration`、`--face-model`、`--onnx-library` 和 `--face-threshold` 参数已移除，请改用配置文件。

健康检查：

```sh
curl http://127.0.0.1:8080/health
```

响应为 `{"status":"ok"}`。使用 Ctrl+C 或发送 SIGTERM 可以优雅停止服务。

## 前端构建与部署

先准备前端所需模型（参见前端说明），再构建前端和 Go 二进制：

```sh
cd frontend
npm ci
npm run build
cd ..
go build -o video-capture .
./video-capture serve
```

Go 在编译时将 `frontend/dist` 嵌入二进制，API 与前端同源。页面导航支持 Vue Router 的 history 模式；未知 API 和不存在的静态资源返回 404。

部署时无需另行分发 `frontend/dist`；配置文件、数据库、后端人脸模型和 ONNX Runtime 共享库等其他依赖仍需按实际配置提供。前端更新后必须重新构建前端并重新编译 Go 二进制。`frontend/dist` 不纳入版本控制，因此新检出项目后运行 `go run`、`go build` 或 `go test ./...` 前必须先运行前端构建，否则编译会失败。人脸识别所需前端模型须在构建前放入 `frontend/public/mediapipe`，才能包含在二进制内。

## 数据库迁移

使用 SQLite 和 sqlx，先执行迁移创建用户表 `users` 和图片上传记录表 `image_uploads`：

```sh
go run . migrate
# 使用其他配置（serve 和 migrate 读取同一个 database.path）
go run . --config /path/to/config.yaml migrate
```

数据库路径由 `database.path` 指定，默认是当前目录下的 `video-capture.db`；父目录必须已存在。迁移记录保存在 `schema_migrations`；再次执行会跳过已完成的迁移。迁移在事务中执行，失败时不会保留部分变更。`image_uploads` 记录所属用户、文件路径、原始文件名、内容类型、大小和表情标签，不存储图片文件本身。删除有上传记录的用户会被外键约束阻止。

迁移不会自动运行。版本 2 对用户名添加精确、区分大小写的唯一索引，并增加加密人脸特征表；如果旧数据库有重复用户名，迁移会失败，需要先处理重复数据。已有用户如果没有人脸特征，注册会拒绝执行，避免跳过旧用户进行查重。

## 实验性人脸注册与验证

**仅供本地实验，不可用于生产环境或作为可靠的防刷/身份验证方案。** 当前没有服务端活体检测，任何人都可能重放或伪造客户端提交的静态图片；数字图片验证码也可能被机器识别。MobileFaceNet 的预处理和默认相似度阈值 `0.6` 尚未用目标场景数据校准，不能保证查重的准确率或公平性。正式使用之前需要经过验证的服务端活体检测、可信采集、模型预处理及阈值校准、可靠的人机验证和限流。

启用此功能需要 CGO、ONNX Runtime 共享库（需支持 API 版本 29，例如 ONNX Runtime 1.30 或 1.31）、仓库中的 `mobile_facenet-onnx-float/mobile_facenet.onnx` 及其同目录 `mobile_facenet.data` 文件。如果启动时提示 ONNX Runtime API 版本不可用，请检查 `face.onnx_library` 指向的共享库版本。设置 `face.enabled` 为 `false` 可关闭人脸认证，只提供健康检查和静态前端；为保持当前行为，默认值为 `true`。向量使用 AES-256-GCM 加密；准备两个独立的随机密钥并优先通过环境变量注入（标准 Base64 编码，解码后均至少 32 字节；加密密钥必须恰好 32 字节）。例如可以分别用 `openssl rand -base64 32` 生成，切勿提交密钥：

```sh
export FACE_ENCRYPTION_KEY='<加密密钥>'
export JWT_SIGNING_KEY='<JWT签名密钥>'
# 在 config.yaml 中设置 face.onnx_library
go run . migrate
go run . serve
```

使用 `face.model_path` 指定模型，`face.onnx_library` 指定共享库，`face.threshold` 指定余弦相似度阈值。密钥也可分别设置在 `face.encryption_key` 和 `auth.jwt_signing_key`，但应保护配置文件权限、不要提交或公开；环境变量 `FACE_ENCRYPTION_KEY` 和 `JWT_SIGNING_KEY` 若已设置（包括空值），优先于 YAML。根目录的 `config.yaml` 已被 Git 忽略，其他路径需要自行保护。模型输入为 RGB、NCHW、像素范围 0–1；客户端必须提供已对齐裁剪的 112×112 JPEG 或 PNG。服务端从图像计算向量，不接受或信任客户端向量，且不持久保存图像。模型文件和密钥须由部署者安全保管；丢失或更换加密密钥会导致现有向量无法比对，必须先规划安全迁移。

接口：

1. `GET /api/captcha` 返回 `captcha_id` 和验证码图片 Data URL；验证码两分钟内有效、只能校验一次。
2. `POST /api/guest`，JSON `{"captcha_id":"...","answer":"..."}`，返回 `guest_token`；有效期五分钟，仅供注册或登录使用。
3. `POST /api/register` 或 `POST /api/login`，带 `Authorization: Bearer <guest_token>` 和 JSON `{"name":"用户名","image":"<图片字节的标准 Base64>"}`。每次尝试都会消耗 Guest Token，包括参数、查重或模型失败；注册成功返回 `user_token`。登录使用用户名及人脸向量匹配已有用户并返回新 token。
4. `GET /api/user` 和 `DELETE /api/user` 使用 `Authorization: Bearer <user_token>`。正式 token 有效期 24 小时；删除用户会级联删除人脸向量并使旧 token 失效，若存在图片上传记录则受外键约束阻止删除。

注册在 SQLite 写事务内串行完成全库比较和用户写入，避免并发注册绕过查重。验证码、Guest Token 和每 IP 限流状态均保存在单进程内存中，重启失效；限流为每个直连 IP 每分钟最多 20 次验证码/令牌请求。不要把此实现当作多实例共享锁或生产级防刷；反向代理部署时不能未经验证地信任转发的 IP，且同一代理后的用户会共享配额。

前端已接入验证码、人脸注册与登录，以及账户查询和删除。开发和部署说明见 `frontend/README.md`。访问接口时应使用 HTTPS，不要记录请求图片、向量或令牌。向量保留至用户删除；SQLite 启用 `secure_delete`，但备份、日志、文件系统快照和存储介质仍可能保留历史数据，需按部署环境另行处理。删除向量后同一人可以重新注册，因此不能同时保证永久去重和彻底删除生物数据。

## 图片上传到阿里云 OSS

使用 [阿里云 OSS Go SDK V2](https://help.aliyun.com/zh/oss/developer-reference/manual-for-go-sdk-v2/) 上传，先运行 `go run . migrate` 应用迁移 3。在 YAML 中启用 OSS，并设置 Bucket 地域与名称：

```yaml
oss:
  enabled: true
  region: "cn-hangzhou"
  bucket: "your-bucket-name"
  endpoint: "" # 可选；留空时 SDK 使用地域对应的公网 HTTPS endpoint
  access_key_id: "your-access-key-id"
  access_key_secret: "your-access-key-secret"
  session_token: "" # 使用 STS 临时凭证时填写
```

上传依赖现有用户验证服务，需要 `face.enabled: true`。自定义 endpoint 必须是 HTTPS origin。凭证可放在 YAML 中，或通过环境变量覆盖：`OSS_ACCESS_KEY_ID`、`OSS_ACCESS_KEY_SECRET`（也支持 `ALIYUN_ACCESS_KEY_ID`、`ALIYUN_ACCESS_KEY_SECRET`）及 `OSS_SESSION_TOKEN`。每项的优先级为 `OSS_*`、`ALIYUN_*`、YAML；已设置的空环境变量也会覆盖配置。使用 STS 时必须提供对应的 session token。包含密钥的配置文件应限制访问权限，且不要提交或公开；根目录 `config.yaml` 已被 Git 忽略。凭证需要目标 Bucket 的 PutObject 和设置对象私有 ACL 权限；建议使用最小权限的 RAM/STS 凭证。服务启动时会检查凭证是否存在，但不会验证其云端权限或 Bucket 是否可用。

`oss.region` 必须填写 Bucket 的完整地域 ID，并与 endpoint 对应；例如河源填写 `cn-heyuan`，而不是 `heyuan` 或 `oss-cn-heyuan`。地域不一致会导致 OSS 返回 `Invalid signing region in Authorization header`。

`POST /api/images` 需要正式用户 JWT，Guest Token 无权上传。请求为 `multipart/form-data`，包含必填 `file` 和可选 `expression`（最多 30 字符）：

```sh
curl -X POST http://127.0.0.1:8080/api/images \
  -H "Authorization: Bearer $USER_TOKEN" \
  -F "file=@photo.jpg" -F "expression=微笑"
```

仅接受真实 JPEG、PNG，文件最大 5 MiB，宽高各不超过 4096、总像素不超过 1600 万。成功返回 HTTP 201 和 `id`、`status`、`content_type`、`size_bytes`、`expression`；对象使用随机 Key 和私有 ACL，不返回公开 URL，也不包含读取或删除接口。照片上传到 OSS 后将按照 Bucket 和账户的配置保留；删除账户目前会因关联上传记录被阻止。

上传前在 `image_uploads` 创建 `pending` 记录，OSS 成功且数据库更新后变为 `uploaded`。网络错误、进程中断或数据库更新失败时，OSS 的实际结果可能不确定；接口会尽可能返回 `upload_id` 和 `pending` 状态，不应直接认为失败后重试不会产生重复图片。运维人员需暂停相关上传，依据记录的 `file_path` 在 OSS 核对对象并手动恢复：对象确认存在则更新状态为 `uploaded`；确认不存在且请求已终止后可删除对应 `pending` 记录。Bucket 版本控制、备份及中断请求可能影响核对与删除，需按实际环境处理。旧图片记录迁移后默认标记为 `uploaded`。

运行测试：

```sh
go test ./...
```
