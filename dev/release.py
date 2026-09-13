"""Package CI binaries with executable modes and hashes of the published archives."""
from pathlib import Path
import hashlib
import tarfile

out = Path('dist')
out.mkdir(exist_ok=True)
checksums = []
for target in ['linux-amd64', 'darwin-arm64', 'darwin-amd64']:
    binary = Path('artifacts') / target / 'mesh'
    binary.chmod(0o755)
    archive = out / f'mesh-{target}.tar.gz'
    with tarfile.open(archive, 'w:gz') as stream:
        stream.add(binary, arcname='mesh')
    checksums.append(hashlib.sha256(archive.read_bytes()).hexdigest() + '  ' + archive.name)
(out / 'SHA256SUMS').write_text('\n'.join(checksums) + '\n')
