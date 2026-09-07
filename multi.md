可以，下面给你整理成一份可以直接存进笔记的完整操作记录。重点把“为什么这么搞、每一步做什么、最后怎么验证”都记下来，之后重装 OpenWrt 也能照着复现。

OpenWrt 双 WAN + mwan3 + 校园网自动认证方案
0. 最终目标

路由器有两条校园网 DHCP 线路：

                ┌── wan  ─── 10.30.202.151 ──┐
LAN ─ OpenWrt ──┤                              ├── Internet
                └── wanb ── 10.30.243.171 ───┘

两条线路：

线路	OpenWrt 接口	物理接口	IP	Gateway
线路 1	wan	wan	10.30.202.151	10.30.0.1
线路 2	wanb	lan2	10.30.243.171	10.30.0.1

目标：

两条线路同时使用
mwan3 进行负载均衡
线路 2 的校园认证由 Go 程序自动完成
不能用 ICMP 判断线路 2 是否正常
改用 HTTP 204 检测公网连通性
线路 2 断网/掉认证时自动从负载均衡中移除
恢复后自动加入
普通 LAN 客户端不需要知道 mwan3 的 mark
1. OpenWrt 基础网络

当前环境：

OpenWrt 25.12.5 ubootmod

LAN：

br-lan
192.168.6.1/24

两个 WAN：

wan
  IP:      10.30.202.151
  Gateway: 10.30.0.1
  MAC:     c2:8a:ee:1b:e4:2b
wanb
  Device:  lan2
  IP:      10.30.243.171
  Gateway: 10.30.0.1
  MAC:     50:a5:5d:91:69:e3

注意：

两个接口可以使用同一个 Gateway 10.30.0.1。

关键不是 gateway 不一样，而是：

wan  → 自己的路由表
wanb → 自己的路由表

mwan3 会负责区分。

2. 安装 mwan3

OpenWrt 25.12 使用 apk：

apk update
apk add mwan3 luci-app-mwan3

检查：

mwan3 status
3. mwan3 两条线路

配置：

wan
wanb

两个 member：

wan_m1_w1
wanb_m1_w1

两个 member 都：

metric = 1
weight = 1

所以正常状态：

wan   50%
wanb  50%

Policy：

balanced

包含：

wan_m1_w1
wanb_m1_w1
4. mwan3 最重要的东西：mark + routing table

mwan3 并不是简单地：

指定网卡

而是：

iptables/nftables mark
        ↓
ip rule
        ↓
routing table
        ↓
对应 WAN

查看：

ip rule show

最终看到类似：

1001: from all iif wan lookup 1
1003: from all iif lan2 lookup 3

2001: from all fwmark 0x100/0x3f00 lookup 1
2003: from all fwmark 0x300/0x3f00 lookup 3

这里最重要：

0x100 → wan
0x300 → lan2 / wanb

因此：

mark 0x100
    ↓
table 1
    ↓
wan

mark 0x300
    ↓
table 3
    ↓
lan2
5. 验证 wanb 的独立路由表

执行：

ip route show table 3

应该看到：

default via 10.30.0.1 dev lan2
10.30.0.0/16 dev lan2
192.168.6.0/24 dev br-lan

然后：

ip route get 1.1.1.1 mark 0x300

正确结果类似：

1.1.1.1 via 10.30.0.1 dev lan2 table 3
src 10.30.243.171
mark 0x300

这一步非常重要。

它证明：

0x300 确实可以把流量送到 lan2。

6. 为什么不能直接 curl --interface lan2

最开始我们测试：

curl --interface lan2 http://www.google.cn/generate_204

结果失败。

原因不是：

lan2 没网

而是：

SO_BINDTODEVICE = lan2

只是在 socket 层绑定网卡。

它不会自动给这个 socket 加 mwan3 所需要的 fwmark。

所以 Linux 还是可能按照：

main routing table

走。

最终出现：

no route
7. 正确测试 wanb 的方法

mwan3 自带：

mwan3 use

所以：

mwan3 use wanb curl -I --max-time 10 http://www.google.cn/generate_204

可以看到类似：

DEVICE=lan2
SRCIP=10.30.243.171
FWMARK=0x3f00

然后：

HTTP/1.1 204 No Content

这证明：

wanb 本身是能够访问公网的。

8. 为什么不能用 ping 判断 wanb

最初配置：

wanb.track_method = ping

例如：

1.1.1.1
114.114.114.114

结果：

ping -I lan2 1.1.1.1
100% packet loss

但是：

ping -I lan2 10.30.0.1

却成功。

后来发现：

校园网 Gateway
      ↓
本地可达
      ↓
公网 ICMP 不一定允许

所以：

ping Gateway

只能证明：

二层/三层到校园网网关正常。

不能证明：

已经认证 + 可以访问公网。

9. 最关键的设计：HTTP 204 检测

使用：

http://www.google.cn/generate_204

正常情况下返回：

HTTP/1.1 204 No Content

因此判断逻辑：

HTTP 204
    ↓
公网正常

而如果校园认证掉了：

请求 generate_204
       ↓
校园认证系统拦截
       ↓
302 / 登录页面 / 非 204
       ↓
认为 wanb 不可用

这个判断比 ping 更符合我们的实际需求。

10. 关闭 mwan3 对 wanb 的 ICMP 检测

执行：

uci set mwan3.wanb.track_ip=''
uci commit mwan3
mwan3 restart

然后：

mwan3 status

看到：

wanb
  tracking disabled

这是正常的。

不要再让 mwan3 自己 ping wanb。

11. Go 自动认证程序

这里是整个方案里另一个关键点。

Go 程序不能只：

SO_BINDTODEVICE=lan2

还需要：

SO_MARK=对应 mwan3 mark

核心逻辑：

找到 wanb
    ↓
找到 lan2
    ↓
读取 ip rule
    ↓
找到对应 routing table
    ↓
找到对应 fwmark
    ↓
SO_MARK
    ↓
SO_BINDTODEVICE

例如最终：

lan2
 ↓
mark 0x300
 ↓
table 3
 ↓
10.30.0.1
 ↓
校园认证

因此 Go 程序就可以真正通过 wanb 发起认证请求。

之前：

SO_BINDTODEVICE

会：

no route to host

加入：

SO_MARK

之后：

登录成功
12. HTTP 自动检测脚本

文件：

/usr/bin/mwan3-http-check.sh

核心配置：

INTERFACE="wanb"
MEMBER="wanb_m1_w1"
URL="http://www.google.cn/generate_204"

检测方式：

mwan3 use wanb curl ...

而不是：

curl --interface lan2

因为：

mwan3 use

会正确设置：

DEVICE
SRCIP
FWMARK
13. 检测逻辑

我们用了：

成功阈值 = 2
失败阈值 = 3

也就是说：

连续成功两次
204
204

认为：

wanb ONLINE

然后：

uci set mwan3.wanb_m1_w1.weight=1
uci commit mwan3
mwan3 restart
连续失败三次

例如：

302
302
000

或者：

000
000
000

则：

wanb OFFLINE

执行：

uci set mwan3.wanb_m1_w1.weight=0
uci commit mwan3
mwan3 restart

于是：

wan   weight 1
wanb  weight 0

实际上就变成：

wan 100%
14. 为什么改 weight，而不是直接 disable wanb

我们没有采用：

uci set mwan3.wanb.enabled=0

因为这样会把：

wanb interface

本身也卷进 mwan3 的状态机。

更干净的方法是：

interface 始终存在
        ↓
wanb 自己负责认证
        ↓
HTTP checker 判断公网
        ↓
只控制 member weight

即：

wanb
 ↓
存在

wanb_m1_w1
 ↓
weight = 1 → 加入负载均衡
weight = 0 → 暂时排除

这样更适合这种校园网。

15. HTTP checker 做成开机服务

创建：

/etc/init.d/mwan3-http-check

使用 procd。

然后：

chmod +x /usr/bin/mwan3-http-check.sh
chmod +x /etc/init.d/mwan3-http-check

开机启动：

/etc/init.d/mwan3-http-check enable

立即启动：

/etc/init.d/mwan3-http-check start

查看：

/etc/init.d/mwan3-http-check status
16. 查看 HTTP checker 日志

：

logread -f | grep mwan3-http-check

正常会看到：

HTTP OK: 204
wanb ONLINE: HTTP 204

例如：

mwan3-http-check: HTTP OK: 204 (2/2)
mwan3-http-check: wanb ONLINE: HTTP 204

如果线路挂了：

mwan3-http-check: HTTP FAIL

连续达到失败阈值后：

wanb OFFLINE
17. 最终 mwan3 状态

正常状态：

mwan3 status

大概是：

wan online
wanb unknown

注意：

wanb unknown
tracking disabled

不用管。

真正应该看的是：

balanced
  wan   50%
  wanb  50%

这说明：

wanb_m1_w1.weight = 1
18. 最终公网测试

普通路由器自身请求：

curl -4 -I --max-time 10 http://www.google.cn/generate_204

应该：

HTTP/1.1 204 No Content

连续测试：

for i in 1 2 3 4 5; do
    curl -4 -s -o /dev/null \
        -w '%{http_code}\n' \
        --max-time 5 \
        http://www.google.cn/generate_204
done

正常：

204
204
204
204
204

你现在已经实际测试成功了。

19. 最终架构

现在整个系统实际上是：

                         ┌───────────────┐
                         │    mwan3      │
                         │   balanced    │
                         └───────┬───────┘
                                 │
                       ┌─────────┴─────────┐
                       │                   │
                    wan 50%             wanb 50%
                       │                   │
                10.30.202.151       10.30.243.171
                       │                   │
                       │                lan2
                       │                   │
                       │             校园认证
                       │                   │
                       │            Go 自动登录
                       │                   │
                       └─────────┬─────────┘
                                 │
                              Internet

线路 2 的健康状态：

              ┌──────────────────┐
              │ HTTP Checker     │
              │ 每 10 秒检测      │
              └────────┬─────────┘
                       │
                       ▼
              generate_204
                       │
             ┌─────────┴─────────┐
             │                   │
          HTTP 204            非 204/失败
             │                   │
        连续 2 次             连续 3 次
             │                   │
             ▼                   ▼
        weight = 1           weight = 0
             │                   │
             ▼                   ▼
          50/50              wan 100%
20. 以后重装/排错的检查清单

如果哪天又出问题，按照这个顺序查，不要一上来乱改 mwan3：

① 看两个接口有没有 IP
ip -4 addr show

确认：

wan  → 10.30.202.x
lan2 → 10.30.243.x
② 看两个 gateway
ip route
③ 看 wanb 独立路由表
ip route show table 3

必须有：

default via 10.30.0.1 dev lan2
④ 看 mwan3 rule
ip rule show

找：

fwmark 0x300/0x3f00 lookup 3
⑤ 强制测试 wanb
mwan3 use wanb curl -4 -I --max-time 10 http://www.google.cn/generate_204

必须：

HTTP/1.1 204
⑥ 看 HTTP checker
logread -f | grep mwan3-http-check
⑦ 看 mwan3
mwan3 status

重点：

balanced:
    wan
    wanb
最终结论

这次真正解决的问题其实不是“两个网卡怎么同时上网”，而是三个东西组合：

① mwan3
   负责多线路策略和负载均衡

② Go 自动认证
   负责让 wanb 通过校园认证

③ HTTP health checker
   负责判断 wanb 到底有没有真正的公网

尤其是最关键的坑：

SO_BINDTODEVICE=lan2 ≠ mwan3 的正确路由。

必须让 wanb 流量进入对应的：

fwmark → routing table

所以 Go 程序最终采用：

SO_MARK + SO_BINDTODEVICE

而 HTTP 检测则直接：

mwan3 use wanb

现在三者已经串起来了。这样以后即使线路 2 掉认证、校园网断线、恢复，整个系统也可以自己处理。

# 创建最终检测脚本

```
cat > /usr/bin/mwan3-http-check.sh <<'EOF'
#!/bin/sh

INTERFACE="wanb"
MEMBER="wanb_m1_w1"
URL="http://www.google.cn/generate_204"

INTERVAL=10
TIMEOUT=8

# 连续成功/失败多少次才切换
UP_COUNT=2
DOWN_COUNT=3

STATE_FILE="/tmp/mwan3-http-check.state"

log() {
    logger -t mwan3-http-check "$*"
}

# 防止重复启动
if [ -f /tmp/mwan3-http-check.pid ]; then
    OLD_PID="$(cat /tmp/mwan3-http-check.pid)"
    if kill -0 "$OLD_PID" 2>/dev/null; then
        exit 0
    fi
fi

echo $$ > /tmp/mwan3-http-check.pid
trap 'rm -f /tmp/mwan3-http-check.pid' EXIT

STATE="down"
SUCCESS=0
FAIL=0

if [ -f "$STATE_FILE" ]; then
    . "$STATE_FILE"
fi

set_weight() {
    NEW_WEIGHT="$1"

    CURRENT="$(uci -q get mwan3.${MEMBER}.weight)"

    if [ "$CURRENT" != "$NEW_WEIGHT" ]; then
        uci set mwan3.${MEMBER}.weight="$NEW_WEIGHT"
        uci commit mwan3

        # mwan3 没有 reload，使用 restart
        mwan3 restart >/dev/null 2>&1

        log "wanb weight changed: $CURRENT -> $NEW_WEIGHT"
    fi
}

check_http() {
    CODE="$(
        mwan3 use "$INTERFACE" \
            curl -4 -sS \
            -o /dev/null \
            -w '%{http_code}' \
            --connect-timeout "$TIMEOUT" \
            --max-time "$TIMEOUT" \
            "$URL" 2>/dev/null
    )"

    [ "$CODE" = "204" ]
}

while true; do

    if check_http; then
        FAIL=0
        SUCCESS=$((SUCCESS + 1))

        log "HTTP OK: 204 (${SUCCESS}/${UP_COUNT})"

        if [ "$SUCCESS" -ge "$UP_COUNT" ]; then
            if [ "$STATE" != "up" ]; then
                STATE="up"
                set_weight 1
                log "wanb ONLINE: HTTP 204"
            fi
        fi

    else
        SUCCESS=0
        FAIL=$((FAIL + 1))

        log "HTTP FAILED (${FAIL}/${DOWN_COUNT})"

        if [ "$FAIL" -ge "$DOWN_COUNT" ]; then
            if [ "$STATE" != "down" ]; then
                STATE="down"
                set_weight 0
                log "wanb OFFLINE: HTTP check failed"
            fi
        fi
    fi

    cat > "$STATE_FILE" <<STATEEOF
STATE="$STATE"
SUCCESS=$SUCCESS
FAIL=$FAIL
STATEEOF

    sleep "$INTERVAL"
done
EOF

chmod +x /usr/bin/mwan3-http-check.sh
```

# 创建开机自启动
```
cat > /etc/init.d/mwan3-http-check <<'EOF'
#!/bin/sh /etc/rc.common

START=99
USE_PROCD=1

start_service() {
    procd_open_instance

    procd_set_param command /usr/bin/mwan3-http-check.sh

    procd_set_param respawn 3600 5 5

    procd_close_instance
}
EOF

chmod +x /etc/init.d/mwan3-http-check
```

# 开机启动 + 立即启动
```
/etc/init.d/mwan3-http-check enable
/etc/init.d/mwan3-http-check start
```

# 看它现在干得怎么样
```
logread -f | grep mwan3-http-check
```

正常情况下你应该很快看到：

mwan3-http-check: HTTP OK: 204 (1/2)
mwan3-http-check: HTTP OK: 204 (2/2)
mwan3-http-check: wanb ONLINE: HTTP 204

然后：

mwan3 status

应该看到：

balanced:
 wanb (50%)
 wan  (50%)
