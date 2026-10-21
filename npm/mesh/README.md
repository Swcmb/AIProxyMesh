# @swcmb/mesh

Mesh 统一网关（OpenCode API + 出口代理池）的 npm 发行版。安装时按平台自动拉取预编译二进制，无需 Go 环境。

```bash
npm install -g @swcmb/mesh
mesh -config config.json
```

## 工作原理

- 主包只含一个 JS 启动器（`bin/mesh.js`）。
- 真正的可执行文件在按平台拆分的 optionalDependencies 包里（如 `@swcmb/mesh-linux-x64`），npm 只会下载与当前 `os`/`cpu` 匹配的那一个。
- 支持平台：linux-x64、linux-arm64、darwin-x64、darwin-arm64、win32-x64、win32-arm64。

## 环境变量

- `MESH_BIN`：跳过平台包解析，直接指定 mesh 可执行文件路径（本地开发或特殊构建时使用）。

## 许可

GPL-3.0-or-later，见包内 LICENSE 与 NOTICE。
