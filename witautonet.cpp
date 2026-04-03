#include <iostream>
#include <string>
#include <vector>
#include <sstream>
#include <map>
#include <fstream>
#include <iomanip>
#include <winsock2.h>
#include <ws2tcpip.h>
#include <windows.h>

// 显式链接库，防止部分编译器报错
#pragma comment(lib, "ws2_32.lib")

// ================= 全局变量 =================
std::string USERNAME = "xxx";
std::string PASSWORD = "xxx";
std::string BASE_HOST = "10.3.16.205";
int BASE_PORT = 9090;
int INTERVAL_MS = 1000;

// ================= 环境初始化 =================
void setup_console() {
    // 强制控制台输出为 UTF-8，解决中文乱码
    SetConsoleOutputCP(CP_UTF8);
    SetConsoleCP(CP_UTF8);
    // 禁用输出缓冲，确保日志实时打印
    std::setvbuf(stdout, NULL, _IONBF, 0);
}

// URL 编码函数
std::string url_encode(const std::string& value) {
    std::ostringstream escaped;
    escaped.fill('0');
    escaped << std::hex;
    for (char c : value) {
        if (isalnum(c) || c == '-' || c == '_' || c == '.' || c == '~') escaped << c;
        else escaped << '%' << std::setw(2) << int((unsigned char)c);
    }
    return escaped.str();
}

// ================= 配置解析 =================
void load_config() {
    const std::string filename = "config.txt";
    std::ifstream infile(filename);
    if (!infile.is_open()) {
        std::ofstream outfile(filename);
        outfile << "username=xxx" << std::endl;
        outfile << "password=125800" << std::endl;
        outfile << "interval_ms=1000" << std::endl;
        outfile.close();
        std::cout << "⚠️ 已生成默认配置文件 config.txt，请修改后重新运行。" << std::endl;
        return;
    }
    std::string line;
    while (std::getline(infile, line)) {
        size_t sep = line.find('=');
        if (sep == std::string::npos) continue;
        std::string k = line.substr(0, sep);
        std::string v = line.substr(sep + 1);
        if (k == "username") USERNAME = v;
        else if (k == "password") PASSWORD = v;
        else if (k == "interval_ms") {
            try { INTERVAL_MS = std::stoi(v); } catch (...) { INTERVAL_MS = 1000; }
        }
    }
}

// 获取本机主要 IP 地址 (Windows)
std::string get_local_ip() {
    char hostname[256];
    if (gethostname(hostname, sizeof(hostname)) == SOCKET_ERROR) return "127.0.0.1";
    struct hostent* host = gethostbyname(hostname);
    if (!host || host->h_addr_list[0] == NULL) return "127.0.0.1";
    return inet_ntoa(*(struct in_addr*)*host->h_addr_list);
}

// ================= 核心逻辑 =================
void do_login() {
    WSADATA wsa;
    if (WSAStartup(MAKEWORD(2, 2), &wsa) != 0) return;

    SOCKET s = socket(AF_INET, SOCK_STREAM, IPPROTO_TCP);
    if (s == INVALID_SOCKET) { WSACleanup(); return; }

    sockaddr_in server;
    server.sin_family = AF_INET;
    server.sin_addr.s_addr = inet_addr(BASE_HOST.c_str());
    server.sin_port = htons(BASE_PORT);

    // 设置连接和接收超时 (2秒)
    int timeout = 2000;
    setsockopt(s, SOL_SOCKET, SO_RCVTIMEO, (char*)&timeout, sizeof(timeout));
    setsockopt(s, SOL_SOCKET, SO_SNDTIMEO, (char*)&timeout, sizeof(timeout));

    if (connect(s, (struct sockaddr*)&server, sizeof(server)) == 0) {
        std::string ip = get_local_ip();
        
        // 构造认证请求
        std::string path = "/api/selfbase/login3";
        std::string query = "?wlanacname=WITPublic2PPPoE&wlanacip=10.3.16.204";
        query += "&wlanuserip=" + ip;
        query += "&username=" + url_encode(USERNAME);
        query += "&userpasswd=" + url_encode(PASSWORD);

        std::string req = "GET " + path + query + " HTTP/1.1\r\n";
        req += "Host: " + BASE_HOST + "\r\n";
        req += "Connection: close\r\n\r\n";

        send(s, req.c_str(), (int)req.length(), 0);

        std::string response;
        char buf[2048];
        int bytes;
        while ((bytes = recv(s, buf, sizeof(buf) - 1, 0)) > 0) {
            buf[bytes] = '\0';
            response += buf;
        }

        // 提取正文内容
        size_t body_pos = response.find("\r\n\r\n");
        std::string body = (body_pos != std::string::npos) ? response.substr(body_pos + 4) : response;

        SYSTEMTIME st; 
        GetLocalTime(&st);
        std::printf("[%02d:%02d:%02d.%03d] IP:%s Result: %s\n", 
                    st.wHour, st.wMinute, st.wSecond, st.wMilliseconds, ip.c_str(), body.c_str());
    } else {
        std::cout << "❌ 无法连接服务器: " << BASE_HOST << std::endl;
    }

    closesocket(s);
    WSACleanup();
}

// ================= 入口函数 =================
int main() {
    // 1. 初始化控制台编码
    setup_console();

    // 2. 加载配置文件
    load_config();

    std::cout << "🚀 Windows 认证后台已启动 (UTF-8)" << std::endl;
    std::cout << "   - 目标: " << BASE_HOST << ":" << BASE_PORT << std::endl;
    std::cout << "   - 间隔: " << INTERVAL_MS << "ms" << std::endl;
    std::cout << "---------------------------------------" << std::endl;

    // 3. 死循环冲锋
    while (true) {
        do_login();
        // Windows 的 Sleep 单位是毫秒
        Sleep(INTERVAL_MS);
    }

    return 0;
}