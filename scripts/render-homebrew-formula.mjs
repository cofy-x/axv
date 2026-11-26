import { fileURLToPath } from 'node:url';
import { validateReleaseVersion } from './package-release.mjs';

const SHA256_RE = /^[0-9a-f]{64}$/;

function assertDigest(label, value) {
  if (typeof value !== 'string' || !SHA256_RE.test(value))
    throw new Error(
      `${label} must be a 64-character lowercase SHA-256 hex digest.`,
    );
}

export async function renderHomebrewFormula({
  version,
  repo = 'cofy-x/axv',
  checksums = {},
}) {
  await validateReleaseVersion(version);
  const tag = `v${version}`;
  const base = `https://github.com/${repo}/releases/download/${tag}`;

  const requiredEntries = Object.entries(checksums).filter(
    ([, digest]) => digest !== undefined && digest !== '',
  );
  if (requiredEntries.length === 0)
    throw new Error('At least one platform SHA-256 checksum is required.');
  for (const [key, digest] of requiredEntries) assertDigest(key, digest);

  const lines = [
    'class Axv < Formula',
    '  desc "Personal cloud workstation for VMs and coding agents"',
    `  homepage "https://github.com/${repo}"`,
    `  version "${version}"`,
    '  license "MIT"',
    '',
  ];

  if (checksums.darwin_arm64 || checksums.darwin_amd64) {
    lines.push('  on_macos do');
    if (checksums.darwin_arm64) {
      lines.push('    on_arm do');
      lines.push(`      url "${base}/axv_${version}_darwin_arm64.tar.gz"`);
      lines.push(`      sha256 "${checksums.darwin_arm64}"`);
      lines.push('    end');
    }
    if (checksums.darwin_amd64) {
      lines.push('    on_intel do');
      lines.push(`      url "${base}/axv_${version}_darwin_amd64.tar.gz"`);
      lines.push(`      sha256 "${checksums.darwin_amd64}"`);
      lines.push('    end');
    }
    lines.push('  end');
    lines.push('');
  }

  if (checksums.linux_amd64 || checksums.linux_arm64) {
    lines.push('  on_linux do');
    if (checksums.linux_arm64) {
      lines.push('    on_arm do');
      lines.push(`      url "${base}/axv_${version}_linux_arm64.tar.gz"`);
      lines.push(`      sha256 "${checksums.linux_arm64}"`);
      lines.push('    end');
    }
    if (checksums.linux_amd64) {
      lines.push('    on_intel do');
      lines.push(`      url "${base}/axv_${version}_linux_amd64.tar.gz"`);
      lines.push(`      sha256 "${checksums.linux_amd64}"`);
      lines.push('    end');
    }
    lines.push('  end');
    lines.push('');
  }

  lines.push('  def install');
  lines.push('    bin.install "axv"');
  lines.push('  end');
  lines.push('');
  lines.push('  test do');
  lines.push(
    `    assert_equal "axv ${version}", shell_output("#{bin}/axv version").strip`,
  );
  lines.push('  end');
  lines.push('end');
  lines.push('');
  return lines.join('\n');
}

function parseArgs(argv) {
  const options = { checksums: {} };
  for (let i = 0; i < argv.length; i += 2) {
    const flag = argv[i];
    const value = argv[i + 1];
    if (!value) throw new Error(`Missing value for ${flag}`);
    switch (flag) {
      case '--version':
        options.version = value;
        break;
      case '--repo':
        options.repo = value;
        break;
      case '--darwin-arm64-sha256':
        options.checksums.darwin_arm64 = value;
        break;
      case '--darwin-amd64-sha256':
        options.checksums.darwin_amd64 = value;
        break;
      case '--linux-amd64-sha256':
        options.checksums.linux_amd64 = value;
        break;
      case '--linux-arm64-sha256':
        options.checksums.linux_arm64 = value;
        break;
      default:
        throw new Error(`Unknown flag: ${flag}`);
    }
  }
  return options;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  try {
    const formula = await renderHomebrewFormula(
      parseArgs(process.argv.slice(2)),
    );
    process.stdout.write(formula);
  } catch (error) {
    console.error(error instanceof Error ? error.message : String(error));
    process.exit(1);
  }
}
