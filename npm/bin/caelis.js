#!/usr/bin/env node

const { spawn } = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');

const exeName = process.platform === 'win32' ? 'caelis.exe' : 'caelis';
const binPath = path.join(__dirname, '..', 'runtime', exeName);

if (!fs.existsSync(binPath)) {
  console.error('[caelis] binary not found at', binPath);
  console.error('[caelis] try: npm rebuild @onslaughtsnail/caelis');
  process.exit(1);
}

const child = spawn(binPath, process.argv.slice(2), {
  stdio: 'inherit',
  env: process.env,
});

child.on('error', (err) => {
  console.error('[caelis] failed to start binary:', err.message);
  process.exit(1);
});

child.on('exit', (code, signal) => {
  if (signal) {
    process.kill(process.pid, signal);
    return;
  }
  process.exit(code === null ? 1 : code);
});
