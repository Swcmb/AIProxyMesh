#!/usr/bin/env node
"use strict";

// 平台二进制解析入口：优先 MESH_BIN 覆盖，否则按当前 os/cpu 解析 optionalDependencies 平台包。
const { spawnSync } = require("node:child_process");

const binaryName = process.platform === "win32" ? "mesh.exe" : "mesh";
let target = process.env.MESH_BIN;

if (!target) {
  const pkg = `@ianwusb/mesh-${process.platform}-${process.arch}`;
  try {
    target = require.resolve(`${pkg}/bin/${binaryName}`);
  } catch {
    console.error(`mesh: 未找到 ${process.platform}-${process.arch} 的平台包 ${pkg}。`);
    console.error("请重新安装 @ianwusb/mesh，或用环境变量 MESH_BIN 指定 mesh 可执行文件路径。");
    process.exit(1);
  }
}

const result = spawnSync(target, process.argv.slice(2), { stdio: "inherit", env: process.env });
if (result.error) {
  console.error(`mesh: 无法启动 ${target}: ${result.error.message}`);
  process.exit(1);
}
process.exit(result.status === null ? 1 : result.status);
