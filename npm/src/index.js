const path = require('path');
const gopm3Path = path.dirname(require.resolve('@hspak/gopm3'));
const binPath = path.resolve(gopm3Path, '..', 'bin', 'gopm3-binary');
const { execFileSync } = require('child_process');

execFileSync(binPath, process.argv.slice(2), { stdio: 'inherit' });
