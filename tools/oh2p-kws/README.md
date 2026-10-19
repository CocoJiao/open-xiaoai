# OH2P KWS 自定义唤醒词 —— 设备侧脚本

配合 [`docs/oh2p-custom-wakeword.md`](../../docs/oh2p-custom-wakeword.md) 使用。

| 文件 | 说明 |
|---|---|
| `run.sh` | 放到设备 `/data/open-xiaoai/kws/run.sh`，由 `/data/init.sh` 看门狗拉起 |

依赖上游 `examples/kws` 的产物（`kws` 二进制 + `models/` + `keywords.txt`），
安装方式见文档第 3 节。

脚本做三件事：

1. 起 `monitor` —— 唤醒时播「在」
2. 起 `polltts` —— 每 0.4 s 向桥接 `GET /next` 取播报文本，用 `tts_play.sh` 读出来
3. 前台跑 `kws`（`noop` 虚拟音频设备）—— 命中关键词就 `GET /wake` 让桥接开麦

桥接地址用环境变量覆盖：

```sh
BRIDGE_HOST=192.168.1.106 BRIDGE_PORT=4401 sh /data/open-xiaoai/kws/run.sh
```
