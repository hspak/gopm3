const path = require('path');
const gopm3Path = path.dirname(require.resolve('@hspak/gopm3'));
const binPath = path.resolve(gopm3Path, '..', 'bin', 'gopm3-binary');
const { execFileSync } = require('child_process');
const assumedConfigPath = `${process.cwd()}/gopm3.config.json`;

// If user passed in an arg, pass that
// Otherwise, assume a path to the config (best effort at project root)
if (process.argv.length > 2) {
  execFileSync(binPath, process.argv.slice(2), { stdio: 'inherit' });
} else {
  execFileSync(binPath, ['-c', assumedConfigPath], { stdio: 'inherit' });
}
