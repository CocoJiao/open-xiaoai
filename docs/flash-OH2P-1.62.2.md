# 小米音箱 Pro（OH2P）1.62.2 刷机实录

> 目标机型：Xiaomi Smart Speaker Pro（型号 `OH2P`，aarch64 内核 + 32 位 armhf 用户态）
> 结果：2026-10-18 23:27 刷入自建 1.62.2 补丁系统，SSH 与自启链路正常，原厂系统保留在 `boot1` 可回滚。
> 本文只记录做法与验证证据，**不含任何密码、账号、令牌**。

---

## 一、整体思路

原厂固件是一个 squashfs 根文件系统。刷机 = 解开 → 打补丁 → 重新打包 → 用 Amlogic 的 USB 工具写回 `system0` 分区。

```
官方固件 (mico_all_*.bin)
   └─ unsquashfs 提取 root.squashfs
        └─ 应用 patches/（放 SSH、禁 OTA、加启动钩子…）
             └─ mksquashfs 重新打包 root-patched.squashfs
                  └─ update partition system0  ← Amlogic USB 工具（1.5~1.8 秒窗口）
                       └─ setenv boot_part boot0 → 重启进补丁系统
```

关键点：**只写 `system0`，`boot1` 保留原厂**，所以刷坏可以回退。

---

## 二、不需要小米账号

`tools/client-patch` 的 OTA 请求签名可以在本地算出来（`internal/ota`），直接向官方 OTA 接口要升级包：

- 请求参数：`model=OH2P`、`version=1.62.2`、`channel=release`、`time=<ms>`
- 签名：`md5(base64(拼接串 + 内置 secret))`
- 返回里带固件直链，下载得到 `mico_all_616cd9d93_1.62.2.bin`（约 37.8 MB）

结论：**刷机全程不需要登录小米账号**（密码登录会被验证码拦住，passToken 也可能失效，都不影响）。

---

## 三、构建补丁固件

```bash
cd tools/client-patch
cp .env.example .env    # 按需填写；.env 已在 .gitignore 中
docker build -t xiaoai-patch:1 .
docker run --rm -v "$PWD":/work -w /work xiaoai-patch:1 bash build.sh
```

### 踩过的四个坑（已在仓库内修复）

| 坑 | 现象 | 修复 |
|---|---|---|
| Go 工具链版本 | `go.mod requires go >= 1.26` | 镜像里设 `GOTOOLCHAIN=auto` |
| `import "uuid"` | 编译报找不到包 | 改为 `github.com/google/uuid`，`go mod tidy` |
| Windows CRLF | `build.sh: $'\r': command not found` | 见 `.gitattributes`：`.sh`/`.patch` 强制 LF |
| `cp -rf $FIRMWARE assets/$FIRMWARE` | 复制成 `assets/<fw>/<fw>/`，**新镜像没覆盖旧文件**，结果刷的是旧固件 | 改为 `cp -f "$FIRMWARE/root-patched.squashfs" ...` |

### squashfs 打包参数要与原厂一致

原厂超级块：`xz`、块大小 131072、`exportable`、**tailends not packed**、无 xattr。
因此打包时**不要**加 `-no-exports` / `-always-use-fragments`：

```diff
- -noappend -all-root -always-use-fragments -no-xattrs -no-exports
+ -noappend -all-root -no-xattrs
```

另外 `patch` 要加 `--no-backup-if-mismatch`，否则镜像里会残留 `.orig` 文件。

---

## 四、产物校验（刷前必做）

| 检查项 | 期望 |
|---|---|
| 文件大小 | 32,079,872 B（分区上限 41,943,040 B，余量 9.86 MB） |
| MD5 | 与构建日志一致，且 `assets/` 下的副本与产物同 MD5 |
| 文件树差异 | 相对原厂**恰好 8 个文件被改、0 增 0 删** |
| `.orig/.rej` | 0 |
| 脚本语法 | 镜像内 `sh -n` 全部通过 |
| 启动链 | `etc/rc.d/S50dropbear`、`K50dropbear` 软链齐全，`dropbear` 配置 `PasswordAuth '1'`、`RootPasswordAuth '1'` |
| 超级块 | 与前面第三节参数一致 |

被改动的 8 个文件：`etc/shadow`、`etc/inittab`、`etc/pam.d/common-auth`、`etc/init.d/dropbear`、`etc/init.d/wireless`、`etc/rc.local`、`bin/ota`、`bin/flash.sh`。

> 说明：`/etc/inittab` 里引用的 `etc/init.d/rcS` 在本机型原厂固件中就不存在，不是被补丁改坏的，不要"修"它。

---

## 五、刷机（Amlogic USB 工具）

### 5.1 不要走 usbipd 转发

实测 USB 枚举窗口只有 **1.46~1.78 秒**，而 `usbipd attach` 稳定耗时 **3.4~3.9 秒**，`--auto-attach` 在设备出现时也不响应 → 这条路判死。

同理 Windows 版 `update.exe` 在本机直接 `0xC000007B` 退出（位深、CRT、驱动都排查过，根因未定），也不要耗时间。

### 5.2 用 Linux 原生 USB（本次采用）

宿主机 Debian 12（x86_64），Amlogic 官方 Linux 版 `update` 二进制 + `libusb-0.1-4`：

```bash
# 上电瞬间执行，命中会打印 AmlUsbIdentifyHost / 版本号
sudo ./update identify

# 命中后一次性做完，不要重复 identify（再 identify 会掉设备）
sudo ./update bulkcmd "setenv bootdelay 15"
sudo ./update bulkcmd "saveenv"
sudo ./update partition system0 ./root_patched.squashfs
sudo ./update bulkcmd "setenv boot_part boot0"
sudo ./update bulkcmd "saveenv"
```

顺序要点：**先写 `system0`，最后才切 `boot_part`**。反过来一旦写入中断，设备就开不了机。

### 5.3 实测日志

```
identify 命中 → partition system0 写入约 8 秒（30 MB）
→ mwrite success / Transfer size 0x1e98000B
→ setenv boot_part boot0 + saveenv
→ 重启进入补丁系统
```

---

## 六、刷后验证

| 项目 | 实测结果 |
|---|---|
| 系统标识 | `Linux OH2P 4.9.61 aarch64`，ROM `1.62.2` |
| 根文件系统 | `/dev/mtdblock4` 挂载为 `squashfs ro` |
| 网络 | `wlan0 192.168.1.181/24`（IP 未变） |
| SSH | `dropbear` 监听 `0.0.0.0:22`，root 可登录 |
| 内存 | 总 241 MB / 可用约 164 MB |
| 原厂进程 | `mpas 8899`、`miio_client 54322` 仍在运行 |
| 补丁文件校验 | `/etc/shadow`、`/etc/init.d/dropbear`、`/bin/ota`、`/etc/rc.local` 四个文件 MD5 与构建产物逐一相同 |

> ⚠️ 刷完请立刻修改 root 密码（补丁里的默认密码只用于首次登录），并确认 `dropbear` 不要暴露到公网。

---

## 七、回滚

原厂系统仍在 `boot1`：

```bash
# 在 1.5 秒窗口内
sudo ./update bulkcmd "setenv boot_part boot1"
sudo ./update bulkcmd "saveenv"
```

---

## 八、已知问题 / 后续

1. **设备侧没有唤醒词引擎**：`apps/client` 与 Rust 客户端都只在系统 `/tmp/open-xiaoai/kws.log` 上做监视，不是 KWS；免唤醒得靠自建引擎或在 ESP32 上做。
2. **默认音频设备是 `noop`**（`apps/client/services/audio/config.go`）：走 PCM 的播放链路默认静音；`apps/chat` 用 `/usr/sbin/tts_play.sh` 出声不受影响。
3. **原厂 OTA 已被补丁禁用**（`download_upgrade()` / `upgrade()` 提前 return），不会把补丁系统覆盖掉。

---

## 九、时间线

| 时间 | 事件 |
|---|---|
| 10-18 白天 | 定位 OTA 签名公式，零账号拿到 1.62.2 官方固件 |
| 10-18 傍晚 | 修构建链（Go/CRLF/cp 嵌套/squashfs 参数），产出 v3 镜像 |
| 10-18 夜 | 6 层静态审计通过 → usbipd 判死 → 改用宿主机原生 USB |
| 10-18 23:24 | identify 命中，写入 `system0` |
| 10-18 23:27 | 补丁系统启动，SSH/自启/WiFi 验证通过 |
