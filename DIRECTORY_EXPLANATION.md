# 目录说明 - 为什么 GitHub 只显示 4 个目录

## 📋 情况说明

您在 GitHub dev 分支看到的 4 个目录是**正常且正确的**。其他目录被故意排除在 Git 仓库之外。

---

## 📁 完整目录对比

### GitHub 上的目录（4个）✅
这些是**源代码目录**，已上传到 GitHub：

1. **cmd/** - 命令行入口程序
2. **internal/** - 内部代码包
3. **scripts/** - 脚本文件
4. **test/** - 测试代码

### 本地额外的目录（4个）🚫
这些是**运行时/临时目录**，已被 `.gitignore` 排除：

1. **bin/** - 编译后的可执行文件
2. **data/** - 运行时数据文件
3. **logs/** - 日志文件
4. **run/** - 运行时临时文件（如 PID）

### 隐藏目录（1个）
- **.git/** - Git 仓库元数据（本地存在，不会上传）

---

## 🔍 为什么这样设计？

### 被排除目录的原因

#### 1. bin/ - 编译产物
```
❌ 不上传原因：
- 二进制文件体积大
- 不同平台需要重新编译
- 可以通过 `make build` 重新生成
- 属于构建产物，不是源代码
```

#### 2. data/ - 数据文件
```
❌ 不上传原因：
- 包含运行时状态快照
- 可能包含敏感交易数据
- 每个环境的数据不同
- 应该在部署时重新生成
```

#### 3. logs/ - 日志文件
```
❌ 不上传原因：
- 日志文件体积大，快速增长
- 包含运行时信息，无需版本控制
- 每个环境的日志不同
- 可能包含敏感信息
```

#### 4. run/ - 运行时文件
```
❌ 不上传原因：
- 包含进程 PID 等临时文件
- 每次运行都会变化
- 属于运行时状态，不是代码
```

---

## ✅ 验证 GitHub 是否完整

### 检查源代码是否完整
您可以在 GitHub dev 分支检查：

1. **Go 源代码** ✅
   - `internal/exchange/adapter.go`
   - `internal/exchange/trade_ws_client.go`
   - 等等...

2. **配置示例** ✅
   - `config.yaml.example`
   - `config.mainnet.yaml`

3. **脚本文件** ✅
   - `scripts/start_production.sh`
   - `scripts/emergency_stop.sh`

4. **文档** ✅
   - `DEPLOYMENT_COMPLETE.md`
   - `WSS_API_RESEARCH.md`
   - `README.md`

### 检查 .gitignore 是否正确
GitHub 上应该有 `.gitignore` 文件，内容包括：
```
bin/
data/
logs/
run/
config.yaml
```

---

## 📊 目录结构对比表

| 目录 | 本地存在 | GitHub存在 | 说明 |
|------|---------|-----------|------|
| **cmd/** | ✅ | ✅ | 源代码 - 已上传 |
| **internal/** | ✅ | ✅ | 源代码 - 已上传 |
| **scripts/** | ✅ | ✅ | 源代码 - 已上传 |
| **test/** | ✅ | ✅ | 源代码 - 已上传 |
| **bin/** | ✅ | ❌ | 编译产物 - 已排除 |
| **data/** | ✅ | ❌ | 数据文件 - 已排除 |
| **logs/** | ✅ | ❌ | 日志文件 - 已排除 |
| **run/** | ✅ | ❌ | 运行时文件 - 已排除 |
| **.git/** | ✅ | ❌ | Git元数据 - 本地 |

---

## 🎯 结论

**GitHub 显示 4 个目录是完全正确的！**

这是标准的 Git 最佳实践：
- ✅ **上传**：源代码、配置示例、文档、脚本
- ❌ **排除**：编译产物、数据文件、日志、运行时文件

---

## 🔧 如何在新环境重建完整目录

如果您从 GitHub 克隆代码，可以这样重建：

```bash
# 1. 克隆代码
git clone https://github.com/newplayman/market-maker-go-v1.0.git
cd market-maker-go-v1.0
git checkout dev

# 2. 创建运行时目录
mkdir -p bin data logs run

# 3. 编译代码（生成 bin/）
make build

# 4. 创建配置文件
cp config.yaml.example config.yaml
# 编辑 config.yaml 填入您的配置

# 5. 运行程序（自动生成 data/、logs/、run/）
./bin/phoenix -config config.yaml
```

---

## 📝 .gitignore 文件内容

以下是完整的 `.gitignore`，确保敏感文件不被上传：

```gitignore
# Binaries
bin/
*.exe
*.exe~
*.dll
*.so
*.dylib

# Test binary
*.test

# Output of the go coverage tool
*.out

# Go workspace file
go.work

# Config files (keep example)
config.yaml

# Data files
data/
*.json
*.db

# Logs
*.log
logs/

# IDE
.vscode/
.idea/
*.swp
*.swo
*~

# OS
.DS_Store
Thumbs.db

# Temporary files
tmp/
temp/
run/
```

---

## ✅ 安全检查清单

确认以下敏感内容**未**上传到 GitHub：

- [x] ❌ API Key（使用环境变量）
- [x] ❌ config.yaml（生产配置）
- [x] ❌ data/ 目录（交易数据）
- [x] ❌ logs/ 目录（日志文件）
- [x] ❌ bin/ 目录（可执行文件）
- [x] ✅ .gitignore（已上传）
- [x] ✅ 源代码（已上传）
- [x] ✅ 文档（已上传）

---

## 🎉 总结

**您的 GitHub 仓库是完整且安全的！**

- 4 个目录（cmd, internal, scripts, test）= 所有**源代码**
- 其他目录被排除 = **运行时文件**，不应该上传
- 这是**标准做法**，符合 Git 最佳实践

任何人从 GitHub 克隆代码后，通过编译和运行，都能重建完整的项目结构。

---
**说明文档生成时间**: 2025-11-28 07:25 UTC
