package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	ConfigFile = "config.json"

	// 用于判断当前线路是否已经通过校园网认证。
	DetectURL = "http://www.google.cn/generate_204"

	// 原程序中的备用 AC。
	FallbackAcIP = "10.3.16.204"

	// 多久检查一次线路。
	CheckInterval = 60 * time.Second

	RequestTimeout = 10 * time.Second

	// Linux socket options
	SOL_SOCKET      = 1
	SO_BINDTODEVICE = 25
	SO_MARK         = 36
)

type Config struct {
	Accounts []Account `json:"accounts"`
}

type Account struct {
	Name      string `json:"name"`
	Interface string `json:"interface"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	Enabled   bool   `json:"enabled"`
}

type LineInfo struct {
	Interface string
	IP        string
	MAC       string
	Gateway   string
	MwanMark  uint32
}

// ============================================================
// Config
// ============================================================

func loadConfig() Config {
	if _, err := os.Stat(ConfigFile); errors.Is(err, os.ErrNotExist) {
		cfg := Config{
			Accounts: []Account{
				{
					Name:      "线路1",
					Interface: "wan",
					Username:  "your_username_1",
					Password:  "your_password_1",
					Enabled:   true,
				},
				{
					Name:      "线路2",
					Interface: "lan2",
					Username:  "your_username_2",
					Password:  "your_password_2",
					Enabled:   true,
				},
			},
		}

		data, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			fmt.Printf("生成配置失败: %v\n", err)
			os.Exit(1)
		}

		if err := os.WriteFile(ConfigFile, data, 0644); err != nil {
			fmt.Printf("创建 [%s] 失败: %v\n", ConfigFile, err)
			os.Exit(1)
		}

		fmt.Printf(
			"=> 未找到 [%s]，已自动创建配置模板。\n",
			ConfigFile,
		)
		fmt.Println("=> 请填写账号密码后重新运行程序。")
		os.Exit(0)
	}

	data, err := os.ReadFile(ConfigFile)
	if err != nil {
		fmt.Printf("读取 [%s] 失败: %v\n", ConfigFile, err)
		os.Exit(1)
	}

	var cfg Config

	if err := json.Unmarshal(data, &cfg); err != nil {
		fmt.Printf(
			"解析 [%s] 失败，请检查 JSON 格式: %v\n",
			ConfigFile,
			err,
		)
		os.Exit(1)
	}

	if len(cfg.Accounts) == 0 {
		fmt.Printf("[%s] 中没有配置任何账号。\n", ConfigFile)
		os.Exit(1)
	}

	return cfg
}

// ============================================================
// mwan3 / policy routing
// ============================================================

// getMwanMark 根据 Linux ip rule 自动寻找某个网卡对应的 mwan3 fwmark。
//
// 例如：
//
//	1001: from all iif wan lookup 1
//	1003: from all iif lan2 lookup 3
//	2001: from all fwmark 0x100/0x3f00 lookup 1
//	2003: from all fwmark 0x300/0x3f00 lookup 3
//
// 那么：
//
//	wan  -> 0x100
//	lan2 -> 0x300
//
// 不需要在 config.json 中手动填写 mark。
func getMwanMark(iface string) (uint32, error) {
	if iface == "" {
		return 0, errors.New("网卡名为空")
	}

	output, err := exec.Command(
		"ip",
		"-4",
		"rule",
		"show",
	).Output()

	if err != nil {
		return 0, fmt.Errorf(
			"执行 ip -4 rule show 失败: %w",
			err,
		)
	}

	lines := strings.Split(
		strings.TrimSpace(string(output)),
		"\n",
	)

	// 第一步：
	// 找到：
	//
	// iif <iface> lookup <table>
	//
	// 从而得到这个接口对应的 mwan3 route table。
	var table string

	for _, line := range lines {
		fields := strings.Fields(line)

		hasIIF := false
		hasLookup := false

		var foundIIF string
		var foundTable string

		for i := 0; i < len(fields); i++ {
			switch fields[i] {
			case "iif":
				if i+1 < len(fields) {
					hasIIF = true
					foundIIF = fields[i+1]
				}

			case "lookup":
				if i+1 < len(fields) {
					hasLookup = true
					foundTable = fields[i+1]
				}
			}
		}

		if hasIIF && hasLookup && foundIIF == iface {
			table = foundTable
			break
		}
	}

	if table == "" {
		return 0, fmt.Errorf(
			"没有找到网卡 %q 对应的 mwan3 route table",
			iface,
		)
	}

	// 第二步：
	// 找到：
	//
	// fwmark <mark>/<mask> lookup <table>
	//
	// 例如：
	//
	// fwmark 0x300/0x3f00 lookup 3
	//
	// 得到 0x300。
	for _, line := range lines {
		fields := strings.Fields(line)

		var markText string
		var lookupTable string

		for i := 0; i < len(fields); i++ {
			switch fields[i] {
			case "fwmark":
				if i+1 < len(fields) {
					markText = fields[i+1]
				}

			case "lookup":
				if i+1 < len(fields) {
					lookupTable = fields[i+1]
				}
			}
		}

		if markText == "" || lookupTable != table {
			continue
		}

		// markText 可能是：
		//
		// 0x100/0x3f00
		//
		// 只需要前面的 mark。
		markOnly := strings.SplitN(
			markText,
			"/",
			2,
		)[0]

		mark, err := strconv.ParseUint(
			markOnly,
			0,
			32,
		)

		if err != nil {
			return 0, fmt.Errorf(
				"解析网卡 %q 的 fwmark %q 失败: %w",
				iface,
				markOnly,
				err,
			)
		}

		return uint32(mark), nil
	}

	return 0, fmt.Errorf(
		"网卡 %q 对应 table %s，但没有找到对应的 fwmark",
		iface,
		table,
	)
}

// ============================================================
// Linux SO_BINDTODEVICE + SO_MARK
// ============================================================

// bindToDevice 将 socket 绑定到指定 Linux 网卡。
func bindToDevice(fd int, iface string) error {
	if iface == "" {
		return errors.New("网卡名为空")
	}

	name := append([]byte(iface), 0)

	_, _, errno := syscall.Syscall6(
		syscall.SYS_SETSOCKOPT,
		uintptr(fd),
		uintptr(SOL_SOCKET),
		uintptr(SO_BINDTODEVICE),
		uintptr(unsafe.Pointer(&name[0])),
		uintptr(len(name)),
		0,
	)

	if errno != 0 {
		return errno
	}

	return nil
}

// setSocketMark 设置 Linux socket 的 SO_MARK。
//
// mwan3 使用这个 mark 来决定本地发出的数据包走哪个
// policy routing table。
func setSocketMark(fd int, mark uint32) error {
	err := syscall.SetsockoptInt(
		fd,
		SOL_SOCKET,
		SO_MARK,
		int(mark),
	)

	if err != nil {
		return err
	}

	return nil
}

// bindSocket 同时设置：
//
// 1. SO_MARK
// 2. SO_BINDTODEVICE
//
// 这样可以保证：
//
// socket → mwan3 policy routing
//
//	→ 指定 route table
//	→ 指定网卡
func bindSocket(
	fd int,
	iface string,
	mark uint32,
) error {

	if mark == 0 {
		return errors.New("mwan3 fwmark 为 0")
	}

	// 先设置 fwmark。
	if err := setSocketMark(fd, mark); err != nil {
		return fmt.Errorf(
			"设置 SO_MARK=0x%x 失败: %w",
			mark,
			err,
		)
	}

	// 再绑定物理网卡。
	if err := bindToDevice(fd, iface); err != nil {
		return fmt.Errorf(
			"绑定网卡 %q 失败: %w",
			iface,
			err,
		)
	}

	return nil
}

// ============================================================
// HTTP Client
// ============================================================

func newBoundClient(
	iface string,
	mark uint32,
) *http.Client {

	dialer := &net.Dialer{
		Timeout: RequestTimeout,

		Control: func(
			network string,
			address string,
			rawConn syscall.RawConn,
		) error {

			var socketErr error

			err := rawConn.Control(func(fd uintptr) {
				socketErr = bindSocket(
					int(fd),
					iface,
					mark,
				)
			})

			if err != nil {
				return fmt.Errorf(
					"访问 socket 失败: %w",
					err,
				)
			}

			if socketErr != nil {
				return fmt.Errorf(
					"线路 %q socket 设置失败: %w",
					iface,
					socketErr,
				)
			}

			return nil
		},
	}

	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		DisableKeepAlives:     true,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          0,
		IdleConnTimeout:       5 * time.Second,
		TLSHandshakeTimeout:   RequestTimeout,
		ResponseHeaderTimeout: RequestTimeout,
		ExpectContinueTimeout: 1 * time.Second,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   RequestTimeout,

		// Portal 的 302 不自动跟随。
		CheckRedirect: func(
			req *http.Request,
			via []*http.Request,
		) error {
			return http.ErrUseLastResponse
		},
	}
}

// ============================================================
// Network information
// ============================================================

func getInterfaceInfo(
	ifaceName string,
) (*LineInfo, error) {

	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		return nil, fmt.Errorf(
			"网卡 %q 不存在: %w",
			ifaceName,
			err,
		)
	}

	var ipv4 string

	addrs, err := iface.Addrs()
	if err != nil {
		return nil, fmt.Errorf(
			"读取网卡地址失败: %w",
			err,
		)
	}

	for _, addr := range addrs {
		switch v := addr.(type) {

		case *net.IPNet:
			if ip := v.IP.To4(); ip != nil {
				ipv4 = ip.String()
			}

		case *net.IPAddr:
			if ip := v.IP.To4(); ip != nil {
				ipv4 = ip.String()
			}
		}

		if ipv4 != "" {
			break
		}
	}

	if ipv4 == "" {
		return nil, errors.New(
			"当前没有 IPv4 地址",
		)
	}

	mark, err := getMwanMark(ifaceName)
	if err != nil {
		return nil, err
	}

	return &LineInfo{
		Interface: ifaceName,
		IP:        ipv4,
		MAC:       iface.HardwareAddr.String(),
		Gateway:   getDefaultGateway(ifaceName),
		MwanMark:  mark,
	}, nil
}

func getDefaultGateway(iface string) string {
	cmd := exec.Command(
		"ip",
		"-4",
		"route",
		"show",
		"default",
		"dev",
		iface,
	)

	output, err := cmd.Output()
	if err != nil {
		return "-"
	}

	fields := strings.Fields(
		string(output),
	)

	for i := 0; i < len(fields)-1; i++ {
		if fields[i] == "via" {
			return fields[i+1]
		}
	}

	return "-"
}

// ============================================================
// Portal
// ============================================================

type Portal struct {
	Account  Account
	Client   *http.Client
	MwanMark uint32
}

func NewPortal(
	account Account,
	mark uint32,
) *Portal {

	return &Portal{
		Account:  account,
		Client:   newBoundClient(account.Interface, mark),
		MwanMark: mark,
	}
}

// Detect:
//
// true  = 当前线路已经联网
// false = 被 Portal 拦截，需要登录
func (p *Portal) Detect() (
	*url.URL,
	bool,
	error,
) {

	req, err := http.NewRequest(
		http.MethodGet,
		DetectURL,
		nil,
	)

	if err != nil {
		return nil, false, err
	}

	req.Header.Set(
		"User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) "+
			"AppleWebKit/537.36 (KHTML, like Gecko) "+
			"Chrome/120.0.0.0 Safari/537.36",
	)

	resp, err := p.Client.Do(req)
	if err != nil {
		return nil, false, err
	}

	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	// Google 204 = 已经联网。
	if resp.StatusCode == http.StatusNoContent {
		return nil, true, nil
	}

	// Portal 通常通过 HTTP Location 返回重定向。
	redirectURLStr := resp.Header.Get(
		"Location",
	)

	// 有些 Portal 把跳转地址塞在 HTML 中。
	if redirectURLStr == "" {
		re := regexp.MustCompile(
			`(?i)url=([^\s"'>]+)`,
		)

		matches := re.FindSubmatch(body)

		if len(matches) > 1 {
			redirectURLStr = string(matches[1])
		}
	}

	if redirectURLStr == "" {
		return nil, false, fmt.Errorf(
			"没有找到 Portal 重定向地址 (HTTP %d)",
			resp.StatusCode,
		)
	}

	targetURL, err := url.Parse(
		redirectURLStr,
	)

	if err != nil {
		return nil, false, fmt.Errorf(
			"解析 Portal URL 失败: %w",
			err,
		)
	}

	return targetURL, false, nil
}

// ============================================================
// Login
// ============================================================

func (p *Portal) Login() bool {
	fmt.Printf(
		"[%s] 正在检测网络...\n",
		p.Account.Name,
	)

	targetURL, online, err := p.Detect()

	if err != nil {
		fmt.Printf(
			"[%s] 探测失败: %v\n",
			p.Account.Name,
			err,
		)
		return false
	}

	if online {
		fmt.Printf(
			"[%s] 网络已连通，无需登录。\n",
			p.Account.Name,
		)
		return true
	}

	query := targetURL.Query()

	query.Set(
		"username",
		p.Account.Username,
	)

	query.Set(
		"userpasswd",
		p.Account.Password,
	)

	if query.Get("wlanusermac") == "" &&
		query.Get("mac") != "" {

		query.Set(
			"wlanusermac",
			query.Get("mac"),
		)
	}

	const apiPath = "/api/selfbase/login3"

	fmt.Printf(
		"[%s] AC = %s\n",
		p.Account.Name,
		query.Get("wlanacip"),
	)

	success, errCode := p.sendLogin(
		targetURL,
		apiPath,
		query,
	)

	if success {
		fmt.Printf(
			"[%s] => 认证成功！\n",
			p.Account.Name,
		)
		return true
	}

	// 保留原来的 -113 fallback。
	if errCode == -113 &&
		FallbackAcIP != "" {

		fmt.Printf(
			"[%s] 原 AC 返回 -113，切换备用 AC [%s]...\n",
			p.Account.Name,
			FallbackAcIP,
		)

		query.Set(
			"wlanacip",
			FallbackAcIP,
		)

		success, _ = p.sendLogin(
			targetURL,
			apiPath,
			query,
		)

		if success {
			fmt.Printf(
				"[%s] => 备用 AC 认证成功！\n",
				p.Account.Name,
			)
			return true
		}
	}

	fmt.Printf(
		"[%s] => 认证失败。\n",
		p.Account.Name,
	)

	return false
}

func (p *Portal) sendLogin(
	targetURL *url.URL,
	apiPath string,
	query url.Values,
) (bool, float64) {

	loginURL := fmt.Sprintf(
		"%s://%s%s?%s",
		targetURL.Scheme,
		targetURL.Host,
		apiPath,
		query.Encode(),
	)

	req, err := http.NewRequest(
		http.MethodGet,
		loginURL,
		nil,
	)

	if err != nil {
		return false, -1
	}

	req.Header.Set(
		"User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) "+
			"AppleWebKit/537.36 (KHTML, like Gecko) "+
			"Chrome/120.0.0.0 Safari/537.36",
	)

	req.Header.Set(
		"Accept",
		"application/json, text/javascript, */*; q=0.01",
	)

	req.Header.Set(
		"Accept-Language",
		"en-US,en;q=0.9,zh-CN;q=0.8,zh;q=0.7",
	)

	req.Header.Set(
		"X-Requested-With",
		"XMLHttpRequest",
	)

	req.Header.Set(
		"Referer",
		targetURL.String(),
	)

	resp, err := p.Client.Do(req)
	if err != nil {
		fmt.Printf(
			"[%s] 登录请求失败: %v\n",
			p.Account.Name,
			err,
		)
		return false, -1
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, -1
	}

	var result map[string]interface{}

	if err := json.Unmarshal(
		body,
		&result,
	); err != nil {

		fmt.Printf(
			"[%s] AC 返回非 JSON: %s\n",
			p.Account.Name,
			strings.TrimSpace(string(body)),
		)

		return false, -1
	}

	var code float64

	if value, ok := result["error_code"].(float64); ok {
		code = value

	} else if value, ok := result["code"].(float64); ok {
		code = value

	} else {
		code = -1
	}

	if code == 0 {
		return true, 0
	}

	if success, ok := result["success"].(bool); ok &&
		success {

		return true, 0
	}

	msg := ""

	if value, ok := result["msg"].(string); ok {
		msg = value
	}

	fmt.Printf(
		"[%s] 服务器响应: %s (错误码: %.0f)\n",
		p.Account.Name,
		msg,
		code,
	)

	return false, code
}

// ============================================================
// Worker
// ============================================================

type Worker struct {
	Account Account
	Portal  *Portal

	mu     sync.RWMutex
	online bool
}

func NewWorker(
	account Account,
	info *LineInfo,
) *Worker {

	return &Worker{
		Account: account,
		Portal: NewPortal(
			account,
			info.MwanMark,
		),
	}
}

func (w *Worker) setOnline(value bool) {
	w.mu.Lock()
	w.online = value
	w.mu.Unlock()
}

func (w *Worker) check() {
	success := w.Portal.Login()

	w.setOnline(success)
}

func (w *Worker) Run(
	stop <-chan struct{},
) {

	// 启动时立即检查。
	w.check()

	ticker := time.NewTicker(
		CheckInterval,
	)

	defer ticker.Stop()

	for {
		select {

		case <-ticker.C:
			w.check()

		case <-stop:
			fmt.Printf(
				"[%s] worker stopped.\n",
				w.Account.Name,
			)
			return
		}
	}
}

// ============================================================
// Main
// ============================================================

func main() {
	fmt.Println("========================================")
	fmt.Println(" Campus Multi-Line Auth")
	fmt.Println("========================================")

	cfg := loadConfig()

	var workers []*Worker

	for _, account := range cfg.Accounts {

		if !account.Enabled {
			continue
		}

		if account.Interface == "" {
			fmt.Println("发现空网卡名，跳过。")
			continue
		}

		if account.Username == "" ||
			strings.HasPrefix(
				account.Username,
				"your_username",
			) {

			fmt.Printf(
				"[%s] 未配置账号，跳过。\n",
				account.Interface,
			)

			continue
		}

		// 获取网卡信息 + 自动获取 mwan3 mark。
		info, err := getInterfaceInfo(
			account.Interface,
		)

		if err != nil {
			fmt.Printf(
				"[%s] 初始化失败: %v\n",
				account.Name,
				err,
			)
			continue
		}

		fmt.Printf(
			"[%s] Interface=%s IP=%s MAC=%s Gateway=%s MwanMark=0x%x\n",
			account.Name,
			info.Interface,
			info.IP,
			info.MAC,
			info.Gateway,
			info.MwanMark,
		)

		workers = append(
			workers,
			NewWorker(account, info),
		)
	}

	if len(workers) == 0 {
		fmt.Println("没有可运行的线路。")
		return
	}

	stop := make(chan struct{})

	sigCh := make(chan os.Signal, 1)

	signal.Notify(
		sigCh,
		syscall.SIGINT,
		syscall.SIGTERM,
	)

	var wg sync.WaitGroup

	for _, worker := range workers {

		wg.Add(1)

		go func(w *Worker) {
			defer wg.Done()

			w.Run(stop)
		}(worker)
	}

	<-sigCh

	fmt.Println()
	fmt.Println("收到退出信号，正在停止...")

	close(stop)

	wg.Wait()

	fmt.Println("程序已退出。")
}
