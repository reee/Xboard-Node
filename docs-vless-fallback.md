# VLESS Fallback 与内置测速站点

## 功能概述

开启后，VLESS + TCP 节点会把**非 VLESS 流量**（如浏览器直接访问节点端口）通过 xray fallback 转发到进程内置的测速站点。测速服务监听 **Linux 抽象 Unix domain socket**，不占用任何 TCP 端口；合法 VLESS 流量仍由 xray 正常代理。

xray inbound 注入形如：

```json
"fallbacks": [{ "type": "unix", "dest": "@xboard-node-<pid>-<hash>", "xver": 1 }]
```

`xver: 1` 使 xray 在转发前附加 PROXY protocol v1 头，测速服务解析后 `/api/ip` 返回真实客户端 IP。

### 流量走向

| 流量类型 | 去向 |
|----------|------|
| 合法 VLESS 流量 | xray 正常代理 |
| 其他 TCP 流量（浏览器等） | 内置测速站点（fallback → 抽象 UDS） |
| REALITY 节点：未通过 REALITY 认证的流量 | REALITY `dest` 伪装站点，**不进入**测速页 |
| REALITY 节点：认证通过但 VLESS 解析失败 | 测速站点 |

## 配置

```yaml
kernel:
  fallback:
    enabled: true            # 默认 true；仅对 VLESS + TCP 节点生效
    title: "Speed Test"
    max_transfer_mb: 256     # 单请求上/下行上限
    max_concurrent: 32       # 并发测速请求上限，超出返回 503
```

| 字段 | 默认 | 说明 |
|------|------|------|
| `enabled` | `true` | 总开关，`false` 时完全不注入 fallback |
| `title` | `Speed Test` | 测速页面标题 |
| `max_transfer_mb` | `256` | 单个 download/upload 请求的字节上限 |
| `max_concurrent` | `32` | download/upload 并发上限，超出返回 `503` |

## 生效条件

以下全部满足才注入 fallback：

| 条件 | 要求 | 不满足时 |
|------|------|----------|
| 节点协议 | `vless` | 不注入 |
| 传输层 `network` | `tcp`（空 = tcp） | ws / grpc / xhttp / h2 不可达，不注入 |
| `decryption` | 空或 `none` | 非 `none` 不注入（xray 限制） |
| `kernel.fallback.enabled` | `true` | 不注入 |

多实例 / 多节点：socket 名由 `config_dir` + 监听端口 + PID 派生，互不冲突。配置热重载（如面板下发节点换端口）时，若 socket 名或 fallback 配置发生变化，先停止旧测速服务再按新值重新绑定；未变化则复用现有服务。`kernel.fallback` 各字段属本地配置，修改需重启进程生效。

## 测速端点

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/` | 测速页面（无外部资源，可直接浏览器访问） |
| GET | `/api/ping` | `{"ok":true}` |
| GET | `/api/download?bytes=N` | N 字节随机数据；默认 1 MiB，超过 `max_transfer_mb` 按上限截断；`bytes` 非法或 ≤0 返回 `400` |
| POST | `/api/upload` | 丢弃请求体，返回 `{"received":N}`；超过 `max_transfer_mb` 返回 `413` |
| GET | `/api/ip` | `{"ip":"..."}`，来自 PROXY protocol v1 的真实客户端 IP |

### curl 验证

HTTPS 节点使用 `curl -k`（跳过证书校验）：

```bash
curl -k https://node.example.com:443/api/ping
curl -k https://node.example.com:443/api/ip
curl -k -o /dev/null "https://node.example.com:443/api/download?bytes=16777216"
head -c 4194304 /dev/zero | curl -k -X POST --data-binary @- https://node.example.com:443/api/upload
```

## 排查

```bash
# 1. 确认抽象 UDS 已监听
ss -xl | grep xboard-node

# 2. 确认日志中有启动记录
#    xray: speedtest fallback started  (带 socket 字段)

# 3. 确认没有额外汇听的 TCP 端口（只有节点端口本身）
ss -ltnp
```

## 边界与安全

- **REALITY**：未通过 REALITY 认证的流量由 xray 转发到 REALITY 的 `dest`（伪装站点），不会进入测速页；只有认证通过但 VLESS 解析失败的流量才走 fallback。
- **非 TCP 传输**：ws / grpc / xhttp / h2 下 fallback 不可达，不注入。
- **`decryption` 非 `none`**：不注入（xray 限制）。
- **Linux only**：抽象 UDS 是 Linux 特性；项目仅构建 Linux。
- **限流**：download/upload 有单请求上限（`max_transfer_mb`）与并发上限（`max_concurrent`，超出 `503`）。
- **无额外端口**：测速服务只监听本机抽象 UDS，无 TCP listener。
- **降级行为**：测速服务绑定失败（如异常场景下的 socket 冲突）时仅记录 Warn 日志，节点代理不受影响，只是没有测速页；下一次生效条件满足的重载会重试绑定。
- **为什么需要 h2c**：xray 服务端在未显式配置 `alpn` 时默认广播 `h2 + http/1.1`，浏览器经 HTTPS 访问节点会协商 h2，fallback 转发的是 TLS 解密后的 HTTP/2 明文；h2c 使测速服务能解析该流量。浏览器本身不会用明文 h2c，该分支只服务 fallback 转发场景。
- **统计隔离**：测速流量不进入任何面板用户流量统计。

## 不在范围

- 面板下发 fallback 配置（仅本地 `kernel.fallback`）
- LibreSpeed 协议兼容
- Trojan 等其他协议的 fallback
