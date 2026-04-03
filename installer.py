import os
import sys
import subprocess
import tkinter as tk
from tkinter import scrolledtext
import threading
import ctypes
import winreg

# 配置
EXE_NAME = "witautonet.exe"
FONT_NAME = "UbuntuSansMono-Regular.ttf"
CONFIG_NAME = "config.txt"
MAX_LOG_LINES = 100

class ArgonSilentUI:
    def __init__(self, root):
        self.root = root
        self.root.title("Argon Auth")
        self.root.geometry("460x400") 
        self.root.configure(bg="#5e72e4") 
        self.root.resizable(False, False)

        self.proc = None
        self.si = subprocess.STARTUPINFO()
        self.si.dwFlags |= subprocess.STARTF_USESHOWWINDOW
        self.si.wShowWindow = subprocess.SW_HIDE
        
        self.setup_resources()
        self.load_custom_font(FONT_NAME)
        
        self.F_BOLD, self.F_REG, self.F_TIPS = ("Ubuntu Sans Mono", 14, "bold"), ("Ubuntu Sans Mono", 10), ("Ubuntu Sans Mono", 9)
        self.C_BLUE, self.C_WHITE, self.C_SUCCESS, self.C_DANGER, self.C_MUTED = "#5e72e4", "#ffffff", "#2dce89", "#f5365c", "#8898aa"

        # --- UI 布局 ---
        self.card_cv = tk.Canvas(root, bg=self.C_BLUE, highlightthickness=0)
        self.card_cv.place(x=30, y=25, width=400, height=340)
        self.draw_round_rect(self.card_cv, 0, 0, 400, 340, 20, fill=self.C_WHITE)

        # 顶部动态提示条
        self.tips_cv = tk.Canvas(root, bg="white", highlightthickness=0, width=340, height=30)
        self.tips_cv.place(x=60, y=40)
        self.tips_bg = self.draw_round_rect(self.tips_cv, 0, 0, 340, 30, 8, fill="#e8ecfa")
        self.tips_txt = self.tips_cv.create_text(170, 15, text="💡 登录成功后直接关闭，内核将在后台常驻", font=self.F_TIPS, fill=self.C_BLUE)

        tk.Label(root, text="ARGON AUTH SYSTEM", font=self.F_BOLD, bg="white", fg=self.C_BLUE).place(x=135, y=80)

        self.create_auto_input("账号", 130, "ent_user")
        self.create_auto_input("密码", 185, "ent_pass", show="*")

        # 状态显示
        self.status_cv = tk.Canvas(root, bg="white", highlightthickness=0, width=300, height=34)
        self.status_cv.place(x=80, y=245)
        self.status_bg = self.draw_round_rect(self.status_cv, 0, 0, 300, 34, 17, fill="#f6f9fc")
        self.status_txt = self.status_cv.create_text(150, 17, text="准备就绪", font=self.F_REG, fill=self.C_MUTED)

        # 动作按钮
        self.action_btn = tk.Label(root, text="开启认证服务", bg=self.C_SUCCESS, fg="white", font=self.F_REG, cursor="hand2")
        self.action_btn.place(x=80, y=300, width=140, height=38)
        self.action_btn.bind("<Button-1>", lambda e: self.toggle_service())

        # 自启动按钮
        self.auto_start_btn = tk.Label(root, text="加载中...", bg="#11cdef", fg="white", font=self.F_REG, cursor="hand2")
        self.auto_start_btn.place(x=240, y=300, width=140, height=38)
        self.auto_start_btn.bind("<Button-1>", lambda e: self.toggle_auto_start())

        # 日志区
        self.log_btn = tk.Label(root, text="TERMINAL LOG ▾", font=self.F_TIPS, bg=self.C_BLUE, fg="white", cursor="hand2")
        self.log_btn.place(x=150, y=375)
        self.log_btn.bind("<Button-1>", lambda e: self.toggle_log())
        self.clear_log_btn = tk.Label(root, text="CLEAR", font=self.F_TIPS, bg=self.C_BLUE, fg="white", cursor="hand2")
        self.clear_log_btn.bind("<Button-1>", lambda e: self.log_area.delete('1.0', tk.END))

        self.log_area = scrolledtext.ScrolledText(root, bg="#172b4d", fg="#e9ecef", font=self.F_REG, bd=0)
        self.log_visible = False

        self.load_config()
        self.check_auto_start_status()

    def draw_round_rect(self, canvas, x1, y1, x2, y2, r, **kwargs):
        points = [x1+r, y1, x2-r, y1, x2, y1, x2, y1+r, x2, y2-r, x2, y2, x2-r, y2, x1+r, y2, x1, y2, x1, y2-r, x1, y1+r, x1, y1]
        return canvas.create_polygon(points, **kwargs, smooth=True)

    def create_auto_input(self, label, y, attr, show=None):
        cv = tk.Canvas(self.root, bg="white", highlightthickness=0, width=300, height=40)
        cv.place(x=80, y=y)
        self.draw_round_rect(cv, 1, 1, 299, 39, 10, fill="white", outline="#dee2e6")
        tk.Label(self.root, text=label, font=self.F_TIPS, bg="white", fg="#adb5bd").place(x=95, y=y+10)
        entry = tk.Entry(self.root, show=show, font=self.F_REG, bd=0, highlightthickness=0, bg="white", fg="#32325d")
        entry.place(x=145, y=y+10, width=220)
        entry.bind("<FocusOut>", lambda e: self.auto_save())
        setattr(self, attr, entry)

    def toggle_service(self):
        if self.proc and self.proc.poll() is None:
            self.stop_service()
        else:
            self.start_service()

    def start_service(self):
        self.auto_save()
        if not os.path.exists(EXE_NAME): return
        self.set_status("内核启动中...", self.C_BLUE)
        threading.Thread(target=self._run, daemon=True).start()
        self.action_btn.config(text="停止认证服务", bg=self.C_DANGER)

    def stop_service(self):
        if self.proc: self.proc.terminate()
        subprocess.run(f'taskkill /F /IM {EXE_NAME}', shell=True, startupinfo=self.si, creationflags=subprocess.CREATE_NO_WINDOW)
        self.proc = None
        self.set_status("服务已停止", "#adb5bd")
        self.action_btn.config(text="开启认证服务", bg=self.C_SUCCESS)

    def _run(self):
        try:
            self.proc = subprocess.Popen([EXE_NAME], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, 
                                   startupinfo=self.si, creationflags=subprocess.CREATE_NO_WINDOW)
            for line_bytes in iter(self.proc.stdout.readline, b''):
                for enc in ['utf-8', 'gbk']:
                    try:
                        line = line_bytes.decode(enc).strip()
                        if line: self.root.after(0, self.write_log, line)
                        break
                    except: continue
        except: pass

    def write_log(self, text):
        lines = int(self.log_area.index('end-1c').split('.')[0])
        if lines > MAX_LOG_LINES: self.log_area.delete('1.0', '2.0')
        self.log_area.insert(tk.END, f"> {text}\n")
        self.log_area.see(tk.END)
        
        # 状态检测逻辑
        if '"error_code":0' in text or 'Result: 0' in text:
            self.set_status("认证成功：在线中 🚀", self.C_SUCCESS)
            self.update_tips("✨ 认证成功！现在可以放心关闭此窗口")
        elif '-210' in text:
            self.set_status("服务就绪：已登录 ✅", self.C_SUCCESS)
            self.update_tips("📢 检测到已登录状态，内核将维持连接")

    def update_tips(self, msg):
        self.tips_cv.itemconfig(self.tips_txt, text=msg)

    def toggle_log(self):
        if not self.log_visible:
            self.root.geometry("460x580")
            self.log_area.place(x=30, y=410, width=400, height=140)
            self.log_btn.config(text="CLOSE LOG ▴")
            self.clear_log_btn.place(x=375, y=375)
        else:
            self.root.geometry("460x400")
            self.log_area.place_forget()
            self.log_btn.config(text="TERMINAL LOG ▾")
            self.clear_log_btn.place_forget()
        self.log_visible = not self.log_visible

    def toggle_auto_start(self):
        key_path = r"Software\Microsoft\Windows\CurrentVersion\Run"
        app_path = os.path.join(os.getcwd(), EXE_NAME)
        try:
            key = winreg.OpenKey(winreg.HKEY_CURRENT_USER, key_path, 0, winreg.KEY_ALL_ACCESS)
            if self.is_auto_start():
                winreg.DeleteValue(key, "ArgonAuthKernel")
            else:
                winreg.SetValueEx(key, "ArgonAuthKernel", 0, winreg.REG_SZ, app_path)
            winreg.CloseKey(key)
            self.check_auto_start_status()
        except: pass

    def is_auto_start(self):
        try:
            key = winreg.OpenKey(winreg.HKEY_CURRENT_USER, r"Software\Microsoft\Windows\CurrentVersion\Run", 0, winreg.KEY_READ)
            winreg.QueryValueEx(key, "ArgonAuthKernel")
            winreg.CloseKey(key)
            return True
        except: return False

    def check_auto_start_status(self):
        if self.is_auto_start():
            self.auto_start_btn.config(text="取消开机自启", bg="#ff9900")
            self.update_tips("✔️ 已开启内核自启动，下次开机将静默运行")
        else:
            self.auto_start_btn.config(text="开启开机自启", bg="#11cdef")
            self.update_tips("💡 提示：开启自启动后，开机无需打开此窗口")

    def auto_save(self):
        u, p = self.ent_user.get(), self.ent_pass.get()
        if u or p:
            with open(CONFIG_NAME, "w", encoding="utf-8") as f:
                f.write(f"username={u}\npassword={p}\ninterval_ms=1000")

    def set_status(self, text, color):
        self.status_cv.itemconfig(self.status_bg, fill=color)
        self.status_cv.itemconfig(self.status_txt, text=text, fill="white")

    def setup_resources(self):
        if hasattr(sys, '_MEIPASS'):
            for f in [EXE_NAME, FONT_NAME]:
                s, t = os.path.join(sys._MEIPASS, f), os.path.join(os.getcwd(), f)
                if not os.path.exists(t) and os.path.exists(s):
                    with open(s, "rb") as fs, open(t, "wb") as ft: ft.write(fs.read())

    def load_custom_font(self, font_path):
        if os.path.exists(font_path): ctypes.WinDLL('gdi32').AddFontResourceExW(font_path, 0x10, 0)

    def load_config(self):
        if os.path.exists(CONFIG_NAME):
            with open(CONFIG_NAME, "r", encoding="utf-8") as f:
                for line in f:
                    if "username=" in line: self.ent_user.insert(0, line.split("=")[1].strip())
                    if "password=" in line: self.ent_pass.insert(0, line.split("=")[1].strip())

if __name__ == "__main__":
    app = tk.Tk()
    ui = ArgonSilentUI(app)
    app.mainloop()