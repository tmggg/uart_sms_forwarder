# MQTT 加密协议

## 兼容范围

- `sms-air780-v1`：报文 `v=1`，原 RSA + AES-256-GCM 协议保持不变。
- `sms-air724-v2`：报文 `v=2`，RSA-PKCS1-v1_5 + AES-256-CBC/PKCS7 + HMAC-SHA256。

在接入页选择设备类型后生成绑定该协议的注册凭据。旧凭据和旧会话的空协议字段按 v1 处理。v1、v2 不能互相尝试或在会话中切换；更换协议需要重新发放凭据。

这只是前后端协议支持，不代表 Air724UG V4035 串口脚本 1.0.1 已支持 MQTT。设备端必须另外实现本协议及 MQTT 传输，并完成真实固件测试。

## v2 会话与编码

设备每次连接生成独立的 32 字节会话根密钥和 16 字节会话 ID。根密钥使用服务器业务 RSA 公钥以 PKCS#1 v1.5 加密，输出 `wrapped_key`。公钥位数见注册配置中的 `rsaBits`，文件路径为 `/lua/server_public.pem`。不得使用 `math.random`、时间戳或 IMEI 直接生成密钥或 IV；设备端安全随机源尚需核实。

所有二进制字段采用小写十六进制。方向为 `up`、`down`，序号范围 1–2147483647，每个方向独立递增。上下行分别派生加密、认证密钥：

```text
enc_key = HMAC-SHA256(root, UTF8("sms-air724-v2|" + sid + "|" + direction + "|enc"))
mac_key = HMAC-SHA256(root, UTF8("sms-air724-v2|" + sid + "|" + direction + "|mac"))
```

HMAC 结果使用 32 字节原始值，不是摘要的十六进制文本。

内层 JSON 与 v1 字段一致，只将 `v` 改为 2：

```json
{"v":2,"device_id":"DEVICE_ID","sid":"32位小写hex","direction":"up","seq":1,"kind":"register","body":{}}
```

用随机 16 字节 IV，AES-256-CBC、PKCS7 填充加密完整内层 JSON（UTF-8）。CBC 的 IV 不能沿用 v1 的可预测方向/序号 nonce。

外层仍为 `v/device_id/sid/direction/seq/kind/iv/ciphertext/tag`；注册额外带 `key_id` 和 `wrapped_key`，其他报文必须省略或置空这两个字段。

## v2 HMAC 输入

按以下固定顺序取外层字段的字符串值：

```text
v, device_id, sid, direction, seq, kind, key_id, wrapped_key, iv, ciphertext
```

`v`、`seq` 使用无前导零的十进制文本。缺省 `key_id`、`wrapped_key` 按空字符串处理。每个字段按 UTF-8 **字节长度**加前缀：`长度:字段值`，再直接串联，不添加空格、换行或其他分隔符。

```text
tag = lowercase_hex(HMAC-SHA256(mac_key, concatenated_length_prefixed_fields))
```

接收端先验证 HMAC，再进行 CBC 解密、PKCS7 检查、JSON 解析及内外字段一致性检查。错误报文统一拒绝，不返回区分标签或填充错误的信息。

## 业务复用

注册 body、事件 message_id、业务 ACK、命令、主题及 QoS 与 v1 保持一致。凭据仍是设备独立的注册秘密；Broker 用户名密码与业务凭据是不同层的认证。新会话通过完整认证后才固定协议并持久化；已退休会话、旧序号和已撤销凭据拒绝处理。

前端保存的注册配置新增 `deviceType`、`cryptoProtocol`、`protocolVersion`、`rsaBits` 和 `publicKeyFile`，原注册凭据、公钥、keyId、主题前缀保留。

## 测试与部署

`internal/fleet/testdata/cbc_vectors.json` 为独立 Python cryptography 实现生成的公开测试向量，不含真实设备密钥或凭据。Go 测试覆盖向量解密、标签/IV/密文和元数据篡改、跨协议拒绝、重放、短信去重、下行、撤销和重启后协议保留；原 GCM/AAD 兼容测试继续保留。

升级会给凭据和会话表增加协议列，旧数据无需重新登记。部署前通过 SQLite backup API 备份数据库，保留配置、RSA 私钥、EMQX 认证和端口映射。不得通过清库或重新生成服务器密钥完成升级。
