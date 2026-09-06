package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"time"
)

const (
	ConfigFile   = "config.json"
	DetectURL    = "http://www.google.cn/generate_204"
	FallbackAcIp = "10.3.16.204"
)

type Config struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func loadOrCreateConfig() Config {
	if _, err := os.Stat(ConfigFile); os.IsNotExist(err) {
		// 文件不存在，创建默认配置模板
		defaultCfg := Config{
			Username: "your_username",
			Password: "your_password",
		}
		data, _ := json.MarshalIndent(defaultCfg, "", "  ")
		_ = os.WriteFile(ConfigFile, data, 0644)
		fmt.Printf("=> 未检测到 [%s]，已自动为你创建，请打开并填入你的账号密码后重新运行。\n", ConfigFile)
		os.Exit(0)
	}

	data, err := os.ReadFile(ConfigFile)
	if err != nil {
		fmt.Printf("读取配置文件失败: %v\n", err)
		os.Exit(1)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		fmt.Printf("解析配置文件失败，请检查 JSON 格式: %v\n", err)
		os.Exit(1)
	}

	if cfg.Username == "your_username" || cfg.Username == "" {
		fmt.Printf("=> 请先在 [%s] 中填入正确的账号和密码！\n", ConfigFile)
		os.Exit(0)
	}

	return cfg
}

func main() {
	cfg := loadOrCreateConfig()

	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	fmt.Println("正在探测网络状态并获取 Portal 参数...")
	resp, err := client.Get(DetectURL)
	if err != nil {
		fmt.Printf("探测请求失败: %v\n", err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == 204 {
		fmt.Println("当前网络已连通外网，无需重复登录。")
		return
	}

	redirectURLStr := resp.Header.Get("Location")
	if redirectURLStr == "" {
		re := regexp.MustCompile(`(?i)url=([^\s"'>]+)`)
		matches := re.FindSubmatch(body)
		if len(matches) > 1 {
			redirectURLStr = string(matches[1])
		}
	}

	if redirectURLStr == "" {
		fmt.Printf("未抓取到重定向参数 (状态码: %d)。\n", resp.StatusCode)
		return
	}

	u, err := url.Parse(redirectURLStr)
	if err != nil {
		fmt.Printf("解析 URL 失败: %v\n", err)
		return
	}

	query := u.Query()
	query.Set("username", cfg.Username)
	query.Set("userpasswd", cfg.Password)
	if query.Get("wlanusermac") == "" && query.Get("mac") != "" {
		query.Set("wlanusermac", query.Get("mac"))
	}

	apiPath := "/api/selfbase/login3"

	fmt.Printf("正在尝试使用原始 AC IP [%s] 登录...\n", query.Get("wlanacip"))
	success, errCode := sendLogin(client, u, apiPath, query)
	if success {
		fmt.Println("=> 认证成功！网络已放行。")
		return
	}

	if errCode == -113 && FallbackAcIp != "" {
		fmt.Printf("原始 AC IP 不可用 (-113)，自动切换至备用真实 AC IP [%s] 重试...\n", FallbackAcIp)
		query.Set("wlanacip", FallbackAcIp)

		success, _ = sendLogin(client, u, apiPath, query)
		if success {
			fmt.Println("=> 认证成功！网络已放行。")
			return
		}
	}

	fmt.Println("=> 认证失败，请检查账号密码或网络状态。")
}

func sendLogin(client *http.Client, targetURL *url.URL, apiPath string, query url.Values) (bool, float64) {
	loginURL := fmt.Sprintf("%s://%s%s?%s", targetURL.Scheme, targetURL.Host, apiPath, query.Encode())

	loginReq, _ := http.NewRequest("GET", loginURL, nil)
	loginReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	loginReq.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	loginReq.Header.Set("Accept-Language", "en-US,en;q=0.9,zh-CN;q=0.8,zh;q=0.7")
	loginReq.Header.Set("X-Requested-With", "XMLHttpRequest")
	loginReq.Header.Set("Referer", targetURL.String())

	loginResp, err := client.Do(loginReq)
	if err != nil {
		return false, -1
	}
	defer loginResp.Body.Close()

	loginBody, _ := io.ReadAll(loginResp.Body)

	var result map[string]interface{}
	if err := json.Unmarshal(loginBody, &result); err == nil {
		code, hasCode := result["error_code"].(float64)
		if !hasCode {
			code, _ = result["code"].(float64)
		}

		if code == 0 || result["success"] == true {
			return true, 0
		}
		fmt.Printf("服务器响应: %v (错误码: %.0f)\n", result["msg"], code)
		return false, code
	}
	return false, -1
}
