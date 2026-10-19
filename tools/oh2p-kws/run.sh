#!/bin/sh
# OH2P 设备侧启动脚本：自定义唤醒词（KWS）+ 唤醒后自动开麦 + 播报轮询
#
# 放在设备：/data/open-xiaoai/kws/run.sh
# 由 /data/init.sh 的看门狗循环拉起：
#   ( while true; do sh /data/open-xiaoai/kws/run.sh >> /tmp/kws-hook.log 2>&1; sleep 5; done ) &
#
# 依赖：
#   1. /data/open-xiaoai/kws/  —— 上游 examples/kws 的产物（kws + models + keywords.txt）
#   2. NAS 上的桥接服务开放两个 HTTP 端口（见 docs/oh2p-custom-wakeword.md）
#        GET /wake  —— 唤醒：让桥接命令设备开始录音
#        GET /next  —— 取一条待播报文本（空则无）

KWS_DIR=/data/open-xiaoai/kws
BRIDGE_HOST=${BRIDGE_HOST:-192.168.1.106}
BRIDGE_PORT=${BRIDGE_PORT:-4401}
WAKE_URL="http://${BRIDGE_HOST}:${BRIDGE_PORT}/wake"
NEXT_URL="http://${BRIDGE_HOST}:${BRIDGE_PORT}/next"

kill -9 $(ps | grep "[o]pen-xiaoai/kws/kws" | awk '{print $1}') 2>/dev/null
kill -9 $(ps | grep "[o]pen-xiaoai/kws/monitor" | awk '{print $1}') 2>/dev/null
kill -9 $(ps | grep "[p]olltts" | awk '{print $1}') 2>/dev/null

# ── 播报轮询 ────────────────────────────────────────────────
# 为什么不直接用桥接的 run_shell 让设备说话：speaker.play() 会 await
# 设备回执，与事件循环互等，实测会把 WebSocket 握手卡死（端口在监听但
# 不再响应）。改成桥接排队、设备主动来取，链路单向，不会再死锁。
cat > /tmp/polltts.sh <<'EOS'
#!/bin/sh
while true; do
  t=$(curl -s -m 2 "$NEXT_URL")
  if [ -n "$t" ]; then
    /usr/sbin/tts_play.sh "$t" >/dev/null 2>&1
  fi
  sleep 0.4
done
EOS
chmod +x /tmp/polltts.sh
/tmp/polltts.sh >/dev/null 2>&1 &

# ── 唤醒应答 ────────────────────────────────────────────────
# 上游 monitor 读 reply.txt 播放，默认是「在」
[ -x "$KWS_DIR/monitor" ] && "$KWS_DIR/monitor" >/dev/null 2>&1 &

# ── KWS 主循环 ──────────────────────────────────────────────
# noop = 使用补丁固件的虚拟音频设备（不要改成 hw:0,3，那是小爱独占的 PCM）
"$KWS_DIR/kws" \
  --model-type=zipformer2 \
  --tokens="$KWS_DIR/models/tokens.txt" \
  --encoder="$KWS_DIR/models/encoder.onnx" \
  --decoder="$KWS_DIR/models/decoder.onnx" \
  --joiner="$KWS_DIR/models/joiner.onnx" \
  --keywords-file="$KWS_DIR/keywords.txt" \
  --provider=cpu \
  --num-threads=1 \
  --chunk-size=1024 \
  noop 2>&1 | while IFS= read -r line; do
    case "$line" in
      *'"keyword"'*)
        echo "[hook] wake"
        curl -s -m 3 "$WAKE_URL" >/dev/null 2>&1
        ;;
    esac
  done
