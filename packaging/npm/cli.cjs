#!/usr/bin/env node
'use strict';
const { spawn } = require('node:child_process');
const { existsSync } = require('node:fs');
const { join } = require('node:path');
const arch = process.arch === 'x64' ? 'amd64' : process.arch;
const binary = join(__dirname, 'bin', `kagero-host-${process.platform}-${arch}`);
if (process.platform !== 'darwin' || !existsSync(binary)) {
  console.error('此安装包不包含当前 Mac 架构。请安装匹配的 Kagero Host 安装包。');
  process.exit(1);
}
const child = spawn(binary, process.argv.slice(2), { stdio: 'inherit' });
child.on('error', () => { console.error('无法启动 Kagero Host，请重新安装。'); process.exitCode = 1; });
child.on('exit', (code) => { process.exitCode = code ?? 1; });
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => child.kill(signal));
