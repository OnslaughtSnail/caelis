import fs from 'node:fs/promises';
import { createWriteStream } from 'node:fs';
import { constants as fsConstants } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import https from 'node:https';
import { pipeline } from 'node:stream/promises';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const packageDir = path.resolve(__dirname, '..');
const runtimeDir = path.join(packageDir, 'runtime');
const packageJsonPath = path.join(packageDir, 'package.json');

const repoOwner = 'OnslaughtSnail';
const repoName = 'caelis';
const skipVar = process.env.CAELIS_NPM_SKIP_DOWNLOAD;

function resolveTarget() {
  const platform = process.platform;
  const arch = process.arch;

  const osMap = {
    darwin: 'darwin',
    linux: 'linux',
    win32: 'windows',
  };
  const archMap = {
    x64: 'amd64',
    arm64: 'arm64',
  };

  const goos = osMap[platform];
  const goarch = archMap[arch];
  if (!goos || !goarch) {
    throw new Error(`unsupported platform/arch: ${platform}/${arch}`);
  }

  return {
    platform,
    goos,
    goarch,
    archiveExt: goos === 'windows' ? 'zip' : 'tar.gz',
    binName: goos === 'windows' ? 'caelis.exe' : 'caelis',
  };
}

async function readPackageVersion() {
	const raw = await fs.readFile(packageJsonPath, 'utf8');
	const pkg = JSON.parse(raw);
	const version = String(pkg.version || '').trim();
	if (!version) {
		throw new Error('invalid package version for release download');
	}
	return version;
}

function buildAssetURL(version, target) {
  const tag = `v${version}`;
  const file = `${repoName}_${version}_${target.goos}_${target.goarch}.${target.archiveExt}`;
  return {
    url: `https://github.com/${repoOwner}/${repoName}/releases/download/${tag}/${file}`,
    filename: file,
  };
}

async function download(url, toFile, redirects = 0) {
	if (redirects > 5) {
		throw new Error(`too many redirects while downloading ${url}`);
	}
	await fs.mkdir(path.dirname(toFile), { recursive: true });
	await new Promise((resolve, reject) => {
		const req = https.get(url, (res) => {
			if ([301, 302, 307, 308].includes(res.statusCode || 0)) {
				const location = String(res.headers.location || '').trim();
				if (!location) {
					reject(new Error(`redirect without location header (${url})`));
					return;
				}
				const nextURL = new URL(location, url).toString();
				res.resume();
				download(nextURL, toFile, redirects + 1).then(resolve).catch(reject);
				return;
			}
			if (res.statusCode !== 200) {
				reject(new Error(`download failed: HTTP ${res.statusCode} (${url})`));
				res.resume();
				return;
			}
			pipeline(res, createWriteStream(toFile)).then(resolve).catch(reject);
		});
		req.on('error', reject);
	});
}

async function extractArchive(archivePath, tempDir, target) {
	if (target.archiveExt === 'zip') {
		const extract = (await import('extract-zip')).default;
		await extract(archivePath, { dir: tempDir });
		return;
	}
	const tar = (await import('tar')).default;
	await tar.x({
		file: archivePath,
		cwd: tempDir,
    gzip: true,
  });
}

async function findFile(rootDir, expectedName) {
  const queue = [rootDir];
  while (queue.length > 0) {
    const current = queue.shift();
    const entries = await fs.readdir(current, { withFileTypes: true });
    for (const entry of entries) {
      const full = path.join(current, entry.name);
      if (entry.isDirectory()) {
        queue.push(full);
        continue;
      }
      if (entry.isFile() && entry.name === expectedName) {
        return full;
      }
    }
  }
  return '';
}

async function installBinary(sourcePath, targetName) {
  await fs.mkdir(runtimeDir, { recursive: true });
  const destPath = path.join(runtimeDir, targetName);
  await fs.copyFile(sourcePath, destPath);
  if (process.platform !== 'win32') {
    await fs.chmod(destPath, 0o755);
  }
  return destPath;
}

async function fileExists(p) {
  try {
    await fs.access(p, fsConstants.F_OK);
    return true;
  } catch {
    return false;
  }
}

async function main() {
  if (skipVar === '1' || skipVar === 'true') {
    console.log('[caelis] skip download due to CAELIS_NPM_SKIP_DOWNLOAD');
    return;
  }

	const version = await readPackageVersion();
	if (version === '0.0.0') {
		console.log('[caelis] skip download for development version 0.0.0');
		return;
	}
	const target = resolveTarget();
  const existingPath = path.join(runtimeDir, target.binName);
  if (await fileExists(existingPath)) {
    return;
  }

  const { url, filename } = buildAssetURL(version, target);
  const tempDir = await fs.mkdtemp(path.join(os.tmpdir(), 'caelis-npm-'));
  const archivePath = path.join(tempDir, filename);

  try {
    console.log(`[caelis] downloading ${url}`);
    await download(url, archivePath);
    await extractArchive(archivePath, tempDir, target);
    const extractedBin = await findFile(tempDir, target.binName);
    if (!extractedBin) {
      throw new Error(`binary ${target.binName} not found in release archive`);
    }
    const installed = await installBinary(extractedBin, target.binName);
    console.log(`[caelis] installed binary: ${installed}`);
  } finally {
    await fs.rm(tempDir, { recursive: true, force: true });
  }
}

main().catch((err) => {
  console.error('[caelis] postinstall failed:', err.message);
  console.error('[caelis] fallback: download release binary manually from GitHub Releases.');
  process.exit(1);
});
