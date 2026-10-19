# OH2P 自定义唤醒词：从「小爱同学」换成任意唤醒词

本文记录在 **小米音箱 Pro（OH2P，自建 1.62.2 补丁系统）** 上，把唤醒词从固定的「小爱同学」
换成自定义唤醒词（示例：「你好小智」），并让唤醒后自动开麦、接入自建 xiaozhi 后端的完整做法。

结论先说：**可行**。唤醒词由设备侧 KWS 独立识别，不再经过小爱的唤醒引擎，
因此电视、手机不会再一起被唤醒，也不会出现「一次提问两个答复」。

---

## 1. 为什么原来的路走不通

| 方案 | 结果 |
|---|---|
| 改小爱唤醒词 | 固件里是固定模型，无接口可改 |
| 停掉 `mico_aivs_lab` 换自己的 KWS 独占麦克风 | 小爱原生功能全废，且 TTS 依赖它重启，有 1–2 秒黑洞 |
| ALSA `hw:0,3` 直接采音 | `Device or resource busy`（小爱独占） |
| ALSA `hw:0,2` | 能录但 −91 dB，不是麦克风通路 |
| 服务端（NAS）KWS | 音箱只在喊「小爱同学」后才上传音频，服务端在此之前没有数据 |

关键点是：**补丁固件提供了 `noop` 虚拟音频设备**，KWS 用 `noop` 作为输入就能拿到非静音音频，
完全不和小爱抢硬件。

## 2. 最终链路

```text
「你好小智」
  ├─ KWS (sherpa-onnx zipformer2, noop 设备)  识别命中
  │    ├─ monitor 播报 reply.txt（「在」）
  │    └─ run.sh 把命中转成 HTTP：GET /wake
  │         └─ 桥接 xiaozhi.py 的向 device 发 start_listening()
  │              └─ 设备收到 start_recording（16k / noop）
  │                   └─ 音频上送 → ASR → LLM
  │                        └─ 回复文本进队列
  └─ run.sh 里的 polltts 每 0.4s 取一次 GET /next
       └─ /usr/sbin/tts_play.sh "<回复>" → 小爱自己的 TTS 发声
```

## 3. 安装步骤（设备侧）

1. 拉取上游 KWS 产物（约 20 MB，设备 `/data` 需预留）：

   ```sh
   curl -sL https://gitee.com/idootop/artifacts/releases/download/open-xiaoai-kws/init.sh -o /tmp/kws-init.sh
   sh /tmp/kws-init.sh --no-monitor    # --no-monitor 只装不跑
   ```

2. 把本目录的 `run.sh` 放到 `/data/open-xiaoai/kws/run.sh`，`chmod +x`。

3. 改 `/data/init.sh`，在客户端看门狗之前加入 KWS 看门狗：

   ```sh
   # 自定义唤醒词：KWS + 唤醒后自动开麦（进程挂了自动拉起）
   if [ -x /data/open-xiaoai/kws/run.sh ]; then
       ( while true; do sh /data/open-xiaoai/kws/run.sh >> /tmp/kws-hook.log 2>&1; sleep 5; done ) &
   fi
   ```

4. 关键词在 `/data/open-xiaoai/kws/keywords.txt`（拼音 tokens 格式），
   阈值用 `--keywords-score` / `--keywords-threshold` 调，默认 0.25。

## 4. 桥接侧需要加的两个 HTTP 入口

桥接（`open-xiaoai-xiaozhi` 容器里的 `xiaozhi.py`）需要：

* 一个线程内的 HTTP 服务（示例监听 4401，容器要 `-p 4401:4401`）
* `GET /wake` → `xiaozhi.start_listening()`：让 .so 向设备发 `start_recording`
* `GET /next` → 从队列里取一条待播报文本（纯文本，空表示没有）

```python
# 播报：只入队，不要调 speaker.play()
def _speak_local(self, text):
    t = (text or "").strip()
    if t:
        self._tts_queue.put(t)

# 取播报（设备端轮询）
if self.path.startswith("/next"):
    try:
        item = self._tts_queue.get_nowait()
    except Exception:
        item = ""
    self._send_plain(item)
```

## 5. 踩过的坑（重要）

### 5.1 不要用 `speaker.play()` 让设备播报

`speaker.play(text=...)` 内部 `await run_shell(...)`，等设备回执。实测在自建桥接里，
这个等待会和事件循环互等：

```text
症状：4399 端口在监听，新连接 TCP 能建立，但 WebSocket 握手永远不返回
      容器不退出、日志无报错，看起来"活着"，实际已经不能服务
触发时机：每次播报完一句之后
```

第一版用 `fut.result(timeout=90)` 在调度线程等 → 卡；第二版改成投递到事件循环也卡。
最终改成**播报不经过桥接的命令通道**：桥接只入队，设备主动 `GET /next` 取走并本地播放。

### 5.2 不要用 WS 握手做健康检查

这个桥接**只服务单连接**：设备连着的时候，第二个连接会被晾着（TCP 能连，握手无响应）。
所以「握手超时」不能作为判死依据，否则看门狗会把正常服务反复重启。

### 5.3 `--no-monitor` 只打印日志

`sh kws-init.sh --no-monitor` 只是不启动 monitor，KWS 会正常识别并把关键词写进日志，
但**没有任何消费者**——表现就是「说了，没反应」。要么带 monitor，要么自己消费 stdout。

### 5.4 BusyBox 环境

* 没有 `nohup`，用 `&` + 重定向
* `timeout` 是 `timeout -t N`，不是 GNU 的 `timeout N`
* 没有 `pgrep`，统一用 `ps | grep "[p]attern"`

## 6. 实测数据（OH2P 1.62.2）

| 项目 | 数值 |
|---|---|
| kws 进程 RSS | 42–45 MB（`--num-threads=1`） |
| monitor RSS | 约 22 MB |
| 整机内存 | 已用约 102 / 241 MB |
| 唤醒→「在」延迟 | < 1 秒 |
| 唤醒→开麦 | < 1 秒（实测设备立刻收到 `start_recording`） |
| 断电重启 | `/data/init.sh` 钩子生效，client / kws / monitor / polltts 全部自启（开机 49 秒时已就绪） |

## 7. 已知限制

* 唤醒后靠服务端 VAD 断句，默认最短静音 200 ms，小孩说话容易被切（建议 400–600 ms）
* 关键词目前是拼音 tokens 手写，加新词要自己转拼音
* `monitor` 的应答词固定在 `reply.txt`
