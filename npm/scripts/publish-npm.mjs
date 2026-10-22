#!/usr/bin/env node
// 从 GitHub Release 产物生成并（可选）发布 npm 二进制包装包集合。
// 用法: node npm/scripts/publish-npm.mjs --tag v0.3.0 --dist release-files [--out npm/out] [--publish]
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

const ROOT = path.resolve(import.meta.dirname, "..", "..");
const SCOPE = "@ianwusb";
const MAIN_NAME = `${SCOPE}/mesh`;

const PLATFORMS = [
  { goos: "linux", goarch: "amd64", npm: "linux-x64", os: "linux", cpu: "x64", binary: "mesh", archive: "tar.gz" },
  { goos: "linux", goarch: "arm64", npm: "linux-arm64", os: "linux", cpu: "arm64", binary: "mesh", archive: "tar.gz" },
  { goos: "darwin", goarch: "amd64", npm: "darwin-x64", os: "darwin", cpu: "x64", binary: "mesh", archive: "tar.gz" },
  { goos: "darwin", goarch: "arm64", npm: "darwin-arm64", os: "darwin", cpu: "arm64", binary: "mesh", archive: "tar.gz" },
  { goos: "windows", goarch: "amd64", npm: "win32-x64", os: "win32", cpu: "x64", binary: "mesh.exe", archive: "zip" },
  { goos: "windows", goarch: "arm64", npm: "win32-arm64", os: "win32", cpu: "arm64", binary: "mesh.exe", archive: "zip" },
];

function parseArgs(argv) {
  const args = { out: path.join(ROOT, "npm", "out"), publish: false };
  for (let i = 0; i < argv.length; i += 1) {
    if (argv[i] === "--tag") args.tag = argv[++i];
    else if (argv[i] === "--dist") args.dist = argv[++i];
    else if (argv[i] === "--out") args.out = path.resolve(argv[++i]);
    else if (argv[i] === "--publish") args.publish = true;
    else throw new Error(`未知参数: ${argv[i]}`);
  }
  if (!args.tag || !args.dist) throw new Error("必须提供 --tag 与 --dist");
  if (!/^v\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$/.test(args.tag)) throw new Error(`非法标签: ${args.tag}`);
  return args;
}

function extract(archive, dest) {
  fs.mkdirSync(dest, { recursive: true });
  if (archive.endsWith(".tar.gz")) {
    execFileSync("tar", ["-xzf", archive, "-C", dest]);
    return;
  }
  try {
    execFileSync("unzip", ["-q", archive, "-d", dest]);
  } catch {
    execFileSync("tar", ["-xf", archive, "-C", dest]);
  }
}

function writeJson(file, value) {
  fs.writeFileSync(file, `${JSON.stringify(value, null, 2)}\n`);
}

function copy(...pairs) {
  for (const [from, to] of pairs) fs.copyFileSync(from, to);
}

function platformPackage({ npm, os: osName, cpu, binary }, version) {
  return {
    name: `${MAIN_NAME}-${npm}`,
    version,
    description: `Mesh 统一网关的 ${osName}/${cpu} 预编译二进制`,
    license: "GPL-3.0-or-later",
    repository: { type: "git", url: "git+https://github.com/Swcmb/AIProxyMesh.git" },
    os: [osName],
    cpu: [cpu],
    files: ["bin", "LICENSE", "NOTICE"],
    publishConfig: { access: "public" },
  };
}

function mainPackage(version) {
  const optionalDependencies = {};
  for (const platform of PLATFORMS) optionalDependencies[`${MAIN_NAME}-${platform.npm}`] = version;
  return {
    name: MAIN_NAME,
    version,
    description: "Mesh：OpenCode 统一网关与出口代理池（预编译二进制发行版）",
    license: "GPL-3.0-or-later",
    repository: { type: "git", url: "git+https://github.com/Swcmb/AIProxyMesh.git" },
    bin: { mesh: "bin/mesh.js" },
    optionalDependencies,
    engines: { node: ">=18" },
    files: ["bin/mesh.js", "README.md", "LICENSE", "NOTICE"],
    publishConfig: { access: "public" },
  };
}

const args = parseArgs(process.argv.slice(2));
const version = args.tag.slice(1);
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "mesh-npm-"));
const packageDirs = [];

try {
  fs.rmSync(args.out, { recursive: true, force: true });
  for (const platform of PLATFORMS) {
    const packageName = `mesh_${args.tag}_${platform.goos}_${platform.goarch}`;
    const archive = path.join(args.dist, `${packageName}.${platform.archive}`);
    if (!fs.existsSync(archive)) throw new Error(`缺少发布产物: ${archive}`);
    const work = path.join(tmp, platform.npm);
    extract(archive, work);
    const binary = path.join(work, packageName, platform.binary);
    if (!fs.existsSync(binary)) throw new Error(`归档内未找到 ${platform.binary}: ${binary}`);

    const pkgDir = path.join(args.out, ...`${MAIN_NAME}-${platform.npm}`.split("/"));
    fs.mkdirSync(path.join(pkgDir, "bin"), { recursive: true });
    fs.copyFileSync(binary, path.join(pkgDir, "bin", platform.binary));
    fs.chmodSync(path.join(pkgDir, "bin", platform.binary), 0o755);
    copy(
      [path.join(ROOT, "LICENSE"), path.join(pkgDir, "LICENSE")],
      [path.join(ROOT, "NOTICE"), path.join(pkgDir, "NOTICE")],
    );
    writeJson(path.join(pkgDir, "package.json"), platformPackage(platform, version));
    packageDirs.push(pkgDir);
  }

  const mainDir = path.join(args.out, ...MAIN_NAME.split("/"));
  fs.mkdirSync(path.join(mainDir, "bin"), { recursive: true });
  copy(
    [path.join(ROOT, "npm", "mesh", "bin", "mesh.js"), path.join(mainDir, "bin", "mesh.js")],
    [path.join(ROOT, "npm", "mesh", "README.md"), path.join(mainDir, "README.md")],
    [path.join(ROOT, "LICENSE"), path.join(mainDir, "LICENSE")],
    [path.join(ROOT, "NOTICE"), path.join(mainDir, "NOTICE")],
  );
  writeJson(path.join(mainDir, "package.json"), mainPackage(version));
  packageDirs.push(mainDir);

  console.log(`已生成 ${packageDirs.length} 个包 → ${args.out}`);
  if (!args.publish) {
    for (const dir of packageDirs) console.log("  -", JSON.parse(fs.readFileSync(path.join(dir, "package.json"))).name);
  } else {
    if (process.env.NPM_TOKEN) {
      const npmrc = path.join(args.out, ".npmrc");
      // 固定官方源：机器默认 registry 可能是国内镜像，令牌只能发给 registry.npmjs.org。
      fs.writeFileSync(npmrc, `registry=https://registry.npmjs.org/\n//registry.npmjs.org/:_authToken=${process.env.NPM_TOKEN}\n`);
    }
    const provenance = process.env.NPM_PROVENANCE === "false" ? [] : ["--provenance"];
    const npmrcArgs = fs.existsSync(path.join(args.out, ".npmrc"))
      ? ["--userconfig", path.join(args.out, ".npmrc")]
      : [];
    // 先发布平台包再发布主包，保证主包 optionalDependencies 可即时解析。
    for (const dir of packageDirs) {
      const name = JSON.parse(fs.readFileSync(path.join(dir, "package.json"))).name;
      console.log(`npm publish ${name}`);
      execFileSync("npm", ["publish", "--access", "public", ...npmrcArgs, ...provenance], {
        cwd: dir,
        stdio: "inherit",
        env: process.env,
      });
    }
  }
} finally {
  fs.rmSync(tmp, { recursive: true, force: true });
}
