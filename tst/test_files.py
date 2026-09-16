"""scp and rsync move real files; an in-flight scp survives a channel cut."""

import shlex
import shutil

import lib
import work_load as workload


def test():
    workload.require('rsync')
    with lib.Lab(['a', 'b', 'r'], {1: ['a', 'b'], 2: ['a', 'r'], 3: ['r', 'b']}) as lab:
        lab.wait_route('a', 'b', ['b'])
        lab.wait_route('r', 'b', ['b'])
        server = workload.SshServer(lab, 'b')
        source, uploaded, returned = [lab.dir / name for name in ('source.bin', 'uploaded.bin', 'returned.bin')]
        digest = workload.random_file(source, 8 << 20)
        transfer = lab.spawn('a', [*server.scp, '-l', '2048', source, f'{server.target}:{uploaded}'],
                             'scp-upload', user=True)
        lab.wait(lambda: uploaded.exists() and uploaded.stat().st_size >= 65536, 'scp started')
        assert transfer.poll() is None, 'scp finished before the cut'
        carried = lab.traffic('r', 'b')
        lab.block('a', 'b')
        lab.wait_route('a', 'b', ['r', 'b'])
        assert transfer.wait(timeout=120) == 0
        assert workload.sha(uploaded) == digest
        assert lab.traffic('r', 'b') > carried
        lab.run('a', [*server.scp, f'{server.target}:{uploaded}', returned], user=True)
        assert workload.sha(returned) == digest

        tree, pushed, pulled = [lab.dir / name for name in ('tree', 'pushed', 'pulled')]
        tree.mkdir()
        (tree / 'empty').mkdir()
        for index in range(12):
            workload.random_file(tree / str(index), (index + 1) * 8192)
        def sync(src, dst):
            lab.run('a', ['rsync', '-a', '--checksum', '--rsync-path=' + shutil.which('rsync'),
                          '-e', shlex.join(server.ssh), src, dst], user=True)
        def manifest(root):
            return {str(p.relative_to(root)): workload.sha(p) if p.is_file() else None for p in root.rglob('*')}
        sync(f'{tree}/', f'{server.target}:{pushed}/')
        assert manifest(tree) == manifest(pushed)
        (tree / '0').write_text('changed after first synchronization\n')
        sync(f'{tree}/', f'{server.target}:{pushed}/')
        sync(f'{server.target}:{pushed}/', f'{pulled}/')
        assert manifest(tree) == manifest(pushed) == manifest(pulled)


lib.main(test)
