# trojan
![](https://img.shields.io/github/v/release/Jrohy/trojan.svg) 
![](https://img.shields.io/docker/pulls/jrohy/trojan.svg)
[![Go Report Card](https://goreportcard.com/badge/github.com/Jrohy/trojan)](https://goreportcard.com/report/github.com/Jrohy/trojan)
[![Downloads](https://img.shields.io/github/downloads/Jrohy/trojan/total.svg)](https://img.shields.io/github/downloads/Jrohy/trojan/total.svg)
[![License](https://img.shields.io/badge/license-GPL%20V3-blue.svg?longCache=true)](https://www.gnu.org/licenses/gpl-3.0.en.html)


trojan多用户管理部署程序

## 功能
- 在线web页面和命令行两种方式管理trojan多用户
- 启动 / 停止 / 重启 trojan 服务端
- 支持流量统计和流量限制
- 命令行模式管理, 支持命令补全
- 集成acme.sh证书申请
- 生成客户端配置文件
- 在线实时查看trojan日志
- 在线trojan和trojan-go随时切换
- 支持trojan://分享链接和二维码分享(仅限web页面)
- 支持转化为clash订阅地址并导入到[clash_for_windows](https://github.com/Fndroid/clash_for_windows_pkg/releases)(仅限web页面)
- 限制用户使用期限
- 用户自助注册: 管理员可开启并设置邀请码, 访客凭邀请码在登录页自助注册, 避免任意访客随意注册(仅限web页面)
- 多平台代理客户端下载入口: 按当前平台推荐常见 clash / trojan 客户端, 并可一键复制订阅或导入 Clash(仅限web页面)

## 架构说明

程序集 **命令行(CLI)** 与 **Web 管理面板** 于同一个二进制:

- `trojan`：直接运行进入交互式管理菜单;
- `trojan web`：以 Web 服务方式启动管理面板(默认 `:80`, 可 `--ssl` 以 https 运行)。

数据存储采用双数据库:

- **MySQL / MariaDB**：存储 trojan 用户(用户名、密码、流量配额、上传/下载、到期时间), 表结构见 `core/mysql.go`;
- **LevelDB**(`/var/lib/trojan-manager`)：存储管理配置类 KV, 如 `admin_pass`、JWT `secretKey`、登录页标题、clash 规则、以及自助注册开关 `register_enabled` 与邀请码 `register_code`。

流量代理与面板的关系(标准部署):

- **trojan 服务端**监听 `:443`(`config.json` 的 `local_port`), 使用域名 TLS 证书;
- 非 trojan 流量被回落转发到 `remote_addr:remote_port`(默认 `127.0.0.1:80`, 即 Web 管理面板);
- 因此浏览器访问 `https://域名` 打开的是管理面板, trojan 客户端连接 `域名:443` 走代理。

主要源码目录:

| 目录 | 说明 |
|------|------|
| `cmd/` | Cobra 命令行子命令 |
| `web/` | Gin HTTP 服务: 路由、JWT 鉴权、controller |
| `web/templates/` | **前端构建产物(由 trojan-web 生成后拷入)**, 通过 `//go:embed` 内嵌进二进制 |
| `core/` | 配置与数据层(config.json、mysql、leveldb) |
| `trojan/` | trojan 安装/证书/用户/客户端配置等业务逻辑 |
| `util/` | 命令执行、字符串、websocket 等工具 |
| `asset/` | 安装脚本、Dockerfile、systemd 服务、clash 规则等内嵌资源 |

前端仓库: [trojan-web](https://github.com/OnionStark/trojan-web)

## 安装方式
*trojan使用请提前准备好服务器可用的域名*  

###  a. 一键脚本安装
```
#安装/更新
source <(curl -sL https://git.io/trojan-install)

#卸载
source <(curl -sL https://git.io/trojan-install) --remove

```
安装完后输入'trojan'可进入管理程序   
浏览器访问 https://域名 可在线web页面管理trojan用户  
前端页面源码地址: [trojan-web](https://github.com/Jrohy/trojan-web)

### b. docker运行
1. 安装mysql  

因为mariadb内存使用比mysql至少减少一半, 所以推荐使用mariadb数据库
```
docker run --name trojan-mariadb --restart=always -p 3306:3306 -v /home/mariadb:/var/lib/mysql -e MYSQL_ROOT_PASSWORD=trojan -e MYSQL_ROOT_HOST=% -e MYSQL_DATABASE=trojan -d mariadb:10.2
```
端口和root密码以及持久化目录都可以改成其他的

2. 安装trojan
```
docker run -it -d --name trojan --net=host --restart=always --privileged jrohy/trojan init
```
运行完后进入容器 `docker exec -it trojan bash`, 然后输入'trojan'即可进行初始化安装   

启动web服务: `systemctl start trojan-web`   

设置自启动: `systemctl enable trojan-web`

更新管理程序: `source <(curl -sL https://git.io/trojan-install)`

## 运行截图
![avatar](asset/1.png)
![avatar](asset/2.png)

## 命令行
```
Usage:
  trojan [flags]
  trojan [command]

Available Commands:
  add           添加用户
  clean         清空指定用户流量
  completion    自动命令补全(支持bash和zsh)
  del           删除用户
  help          Help about any command
  info          用户信息列表
  log           查看trojan日志
  port          修改trojan端口
  restart       重启trojan
  start         启动trojan
  status        查看trojan状态
  stop          停止trojan
  tls           证书安装
  update        更新trojan
  updateWeb     更新trojan管理程序
  version       显示版本号
  import [path] 导入sql文件
  export [path] 导出sql文件
  web           以web方式启动

Flags:
  -h, --help   help for trojan
```

## 从源码构建

由于 Web 面板的前端产物通过 `//go:embed web/templates/*` 内嵌进二进制, 完整构建需先构建前端, 再编译 Go。

### 环境要求
- Go 1.21+
- Node.js 16+ 与 npm
- (运行时)MySQL / MariaDB

### 步骤

1. 构建前端(在 [trojan-web](https://github.com/OnionStark/trojan-web) 仓库):
```bash
cd trojan-web
npm install
npm run build          # 产出 dist/ (index.html + static/)
```

2. 把前端产物拷入后端的 `web/templates/`:
```bash
# 在 trojan 与 trojan-web 同级目录下
rm -rf trojan/web/templates
cp -r trojan-web/dist trojan/web/templates
```

3. 编译后端二进制(按目标服务器架构):
```bash
cd trojan
# x86_64:
GOOS=linux GOARCH=amd64 go build -o trojan-linux-amd64 .
# ARM64:
GOOS=linux GOARCH=arm64 go build -o trojan-linux-arm64 .
```
仓库根目录的 `build.sh` 封装了上述交叉编译并注入版本信息, 可直接使用。

> 说明: 生产版 `index.html` 通过公共 CDN 加载 vue / element-plus 等依赖(见 trojan-web 的 `vite.config.js` 中的 `externalGlobals`), 因此最终用户浏览器需能访问这些 CDN。

## 部署

### 方式一: 全新服务器一键安装(官方发行版)
```bash
source <(curl -sL https://git.io/trojan-install)
```
适用于使用官方预编译二进制。若使用本仓库的自定义构建, 请用下面的方式二。

### 方式二: 部署自定义构建到服务器

前置条件:
- 一个解析到服务器公网 IP 的**域名**(A 记录), 云安全组放行 **80 / 443**;
- 已安装并运行 MySQL / MariaDB, 且存在数据库(默认 `trojan`)。

1. 上传并安装二进制:
```bash
# 备份现有二进制便于回滚
cp /usr/local/bin/trojan /usr/local/bin/trojan.bak 2>/dev/null || true
# 上传对应架构的二进制到服务器后:
install -m 0755 trojan-linux-amd64 /usr/local/bin/trojan
```

2. 安装 systemd 服务并启动 Web 面板(参见 `asset/trojan-web.service`):
```bash
cp asset/trojan-web.service /etc/systemd/system/trojan-web.service
systemctl daemon-reload
systemctl enable --now trojan-web           # 默认监听 :80
systemctl status trojan-web
```

3. 安装并配置 trojan 服务端(首次):
```bash
trojan update           # 安装 trojan 服务端二进制 + 创建 trojan systemd 服务
trojan tls              # 用 acme.sh 为域名申请证书(交互式, 需域名已解析到本机)
trojan start            # 启动 trojan 服务端(监听 443)
systemctl enable trojan # 设为开机自启
```

4. 验证:
   - 浏览器访问 `https://你的域名` 应打开管理面板;
   - 用管理员账号登录后即可添加用户、开启自助注册、下载客户端;
   - trojan 客户端连接 `你的域名:443` + 用户密码即可使用。

### 更新已部署的服务
重新构建后替换二进制并重启即可:
```bash
install -m 0755 trojan-linux-amd64 /usr/local/bin/trojan
systemctl restart trojan-web
```

> 数据库表结构不随本项目功能改动而变化; 自助注册开关/邀请码等配置保存在 LevelDB(`/var/lib/trojan-manager`), 升级二进制无需数据迁移。

## 注意
安装完trojan后强烈建议开启BBR等加速: [one_click_script](https://github.com/jinwyp/one_click_script)  

## Thanks
感谢JetBrains提供的免费GoLand  
[![avatar](asset/jetbrains.svg)](https://jb.gg/OpenSource)
