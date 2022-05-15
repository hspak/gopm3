// Just install for darwin for sake of simplicity, sorry.
// Naively include both am64/arm64 arch in the node package.

// maps process.arch to GOARCH
let GOARCH_MAP = {
  'x64': 'amd64',
  'arm64': 'arm64',
};

let GOOS_MAP = {
  'linux': 'linux',
  'darwin': 'darwin',
};

if (!(process.arch in GOARCH_MAP)) {
  console.error(`Sorry this is only packaged for ${GOARCH_MAP} at the moment.`);
  process.exit(1);
}

if (!(process.platform in GOOS_MAP)) {
  console.error(`Sorry this is only packaged for ${GOOS_MAP} at the moment.`);
  process.exit(1);
}

const arch = GOARCH_MAP[process.arch];
const platform = GOOS_MAP[process.platform];
const installTarget = `gen-gopm3-${platform}-${arch}`;

// "Install"
const { exec } = require('child_process');
exec(`cp bin/${installTarget} bin/gopm3-binary`, (err) => {
  if (err) {
    console.error(err);
    process.exit(1);
  }
});

// Clean up the rest
exec(`rm -f bin/gen-*`, (err) => {
  if (err) {
    console.error(err);
    process.exit(1);
  }
});
