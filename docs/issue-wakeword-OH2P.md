## 背景

目标是把小米音箱 Pro（OH2P）作为家里的**主力语音入口**。家里还有电视、手机等设备同样响应「小爱同学」唤醒词，会一起被唤醒、抢答，因此**必须换成一个专属唤醒词**（例如「你好小智」「嘿贾维斯」），而不是继续依赖小爱自带的唤醒。

当前状态（已验证跑通）：OH2P 已刷入自建 1.62.2 补丁系统（root SSH + 开机自启 + OTA 已禁用），Go 客户端（armv7）运行在 `/data/open-xiaoai/client`，通过桥接容器接到 NAS 上的小智服务器，能实现「小爱同学 → ASR 文本 → 小智 → 逐句 tts_play.sh 播报」。

问题只剩一个：**设备侧没有可用的唤醒词引擎**，所以做不到自定义唤醒词。

---

## 设备实测参数

| 项 | 实测值 |
|---|---|
| CPU | Amlogic，4 核 Cortex-A53（`CPU part 0xd03`） |
| 内存 | 总 241 MB，可用约 143 MB，无 swap |
| 内核 / 用户态 | Linux 4.9.61 aarch64 / **32 位 armhf**（glibc 2.25） |
| 根文件系统 | `/dev/mtdblock4` squashfs **只读** |
| 音频工具 | `arecord` / `aplay` / `amixer` 均可用 |
| 声卡 | card0 `AML-AXGSOUND`（TDM-A/B/C、PDM）、card1 `UAC2_Gadget` |
| PCM 设备 | `hw:0,0` TDM-A、`hw:0,1` TDM-B、`hw:0,2` TDM-C(acm8625p)、`hw:0,3` PDM |

### 关键实测：麦克风被小爱独占

```text
# arecord -D hw:0,2 -f S16_LE -r 48000 -c 1 -d 3 /tmp/x.wav   → 成功，288044 字节
# ffmpeg volumedetect                                          → mean_volume: -91.0 dB（数字静音）

# arecord -D hw:0,3 ...                                        → audio open error: Device or resource busy
```

结论：**PDM（真正的麦克风）被小米自己的语音服务持有**（`mico_aivs_lab`），我们只能打开 TDM-C，但那条通路没有麦克风信号，录出来是纯静音。

---

## 已尝试的方案与结果

| # | 方案 | 结果 | 失败原因 |
|---|---|---|---|
| 1 | 用 `apps/client`（Go 客户端）自带能力做唤醒 | ❌ | 客户端**没有 KWS 引擎**；`kws.go` 只是监视系统写的 `/tmp/open-xiaoai/kws.log`，而该文件无人写入 |
| 2 | 换 Rust 客户端（xiaozhi release 版） | ❌ | 依赖里只有 tokio/serde/uuid，同样无 KWS |
| 3 | 桥接层 KWS（`idootop/open-xiaoai-xiaozhi` 镜像） | ❌ | 它的 KWS 设计上面向"电脑/CLI 麦克风"：两处 `if not get_env("CLI"): return` 门控；开 `CLI=1` 后它会去读**容器内**并不存在的麦克风，拿不到任何音频帧 |
| 4 | 桥接层用 detect 文本注入（现状方案） | ⚠️ 可用但受限 | 必须先喊「小爱同学」；且电视/手机会一起响应 |
| 5 | 客户端音频走 PCM 到真实声卡 | ❌ 未成功 | 客户端默认音频设备是 **`PCM: "noop"`**（`apps/client/services/audio/config.go:13`），音频被路由到空设备；`apps/chat` 出声靠 `/usr/sbin/tts_play.sh`，不碰 PCM，所以"能听到 TTS、听不到流式音频" |
| 6 | 设备侧 `arecord` 直接抓麦克风喂 KWS | ❌ | PDM 被占用（busy）；TDM-C 可打开但无信号（-91 dB） |
| 7 | 网页端 KWS（sherpa-onnx 在 NAS 容器里跑） | ✅ 已跑通 | 但用的是**电脑麦克风**，不是音箱麦克风；不能解决音箱唤醒词问题 |

---

## 需要解决的核心问题

1. **如何拿到音箱麦克风的音频流**（不与小爱抢占，或能被允许共享）
2. 拿到后，在一台 **4 核 A53 / 240MB / 32 位 armhf / glibc 2.25** 的设备上跑一个**低资源唤醒词引擎**（中英文皆可，目标是"你好小智"或自定义词）
3. 唤醒后要能接管会话：暂停/绕过小爱自己的识别，把音频送给我们自己的服务端，并让小爱只负责出声（TTS）

---

## 候选方向（待验证）

| 方向 | 思路 | 需要验证 |
|---|---|---|
| A. 停掉小爱语音服务后抓 PDM | `mico_aivs_lab` 停掉 → PDM 释放 → `arecord` 抓 16k 单声道喂 KWS | 停掉后 `tts_play.sh` 是否还能出声；能否只停"识别"保留"播放" |
| B. ALSA dsnoop 共享 PDM | 配置 `dsnoop` 让多进程共享同一麦克风 | 固件 alsa-lib 是否支持、内核是否允许 |
| C. 从小爱内部管线旁路取音 | 通过 ubus/媒体服务拿到已解码的 PCM | 是否存在这样的接口（`mpas`/`miio_client` 有无暴露） |
| D. 外接 USB 麦克风 | 用 UAC2 gadget / USB 声卡作为独立输入 | OH2P 是否支持 USB host / 供电 |
| E. 客户端内嵌 KWS 并改用真实 PCM 设备 | patch `apps/client` 的 `PCM: noop` → `hw:0,x`，同时内嵌 sherpa-onnx 或 porcupine | 需要能打开可用麦克风（回到问题 1）；armhf 静态编译 sherpa-onnx |
| F. 维持「小爱同学」+ 用声纹/设备门控去重 | 接受共享唤醒词，靠声纹或音频指纹区分"该谁答" | 电视/手机的抢答无法从音箱侧阻止，治标不治本 |

---

## 验收标准

- 对音箱说自定义唤醒词（不喊「小爱同学」）→ 音箱有应答（提示音或"我在"）
- 唤醒后能正常对话，回复由音箱播出
- 唤醒引擎常驻内存占用 < 60 MB，CPU 空闲时占用 < 10%
- 不破坏现有能力：小爱原生 TTS 播报、蓝牙/音乐、OTA 禁用状态

---

## 附：现有实现位置

- 客户端：`apps/client`（Go，GOARCH=arm GOARM=7）
- 桥接：`/vol3/1000/docker/xiaozhi-xiaozhi`（Docker，端口 4399，文本 detect 注入）
- 服务端：xiaozhi-server（NAS，8000 WS / 999 OTA），ASR=FunASR(SenseVoiceSmall, zh)，TTS=EdgeTTS，LLM=OpenAI 兼容
- 设备：root SSH 可用（dropbear 0.0.0.0:22），`/data` 可写，`/data/init.sh` 由 `rc.local` 拉起客户端
