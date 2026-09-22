#!/usr/bin/env python3
"""Build-time source and artifact manifest. No runtime dependency on Python."""
import hashlib, json, pathlib, platform, plistlib, subprocess, sys


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def sources(root):
    entries = {}
    for name in ('go.mod', 'go.sum', 'LICENSE', 'NOTICE', 'DEPENDENCIES.txt', 'SOURCE_ORIGIN.json', 'product', 'internal/safeusb',
                 'internal/smsreceive', 'internal/smsarchive', 'pkg/smscodec',
                 'vendor', 'docs/djisms'):
        path = root / name
        files = [path] if path.is_file() else sorted(path.rglob('*')) if path.is_dir() else []
        for file in files:
            if file.is_symlink():
                raise SystemExit('Symlinks are not accepted in the frozen source')
            if file.is_file() and file.name != '.DS_Store' and '__pycache__' not in file.parts:
                entries[file.relative_to(root).as_posix()] = digest(file)
    canonical = json.dumps(entries, sort_keys=True, separators=(',', ':')).encode()
    return {'sha256': hashlib.sha256(canonical).hexdigest(), 'files': entries}


def main():
    mode, root_arg = sys.argv[1:3]
    root = pathlib.Path(root_arg).resolve()
    source = sources(root)
    if mode == 'source':
        print(source['sha256'])
        return
    if mode != 'build' or len(sys.argv) != 6:
        raise SystemExit('manifest.py source ROOT | build ROOT APP GO EXPECTED_SOURCE')
    app = pathlib.Path(sys.argv[3]).resolve()
    if source['sha256'] != sys.argv[5]:
        raise SystemExit('Source changed during compilation; refuse to freeze this build')
    info = plistlib.loads((app/'Contents/Info.plist').read_bytes())
    files = {p.relative_to(app).as_posix(): digest(p) for p in sorted(app.rglob('*')) if p.is_file()}
    def output(*args):
        return subprocess.check_output(args, text=True).strip()
    data = {'schema': 1, 'version': info['DJISMSVersion'], 'build_id': info['DJISMSBuildID'],
            'bundle_build': info['CFBundleVersion'], 'source': source, 'artifact_files': files,
            'toolchain': {'go': output(sys.argv[4], 'version'), 'swift': output('/usr/bin/xcrun', 'swiftc', '--version'),
                          'sdk': output('/usr/bin/xcrun', '--show-sdk-version'), 'macos': platform.mac_ver()[0]},
            'architecture': 'arm64', 'deployment_target': '14.0', 'signature': 'ad-hoc-development',
            'notarized': False, 'runtime_dependencies': 'macOS system frameworks and libraries only'}
    (app.parent/'build-manifest.json').write_text(json.dumps(data, ensure_ascii=False, indent=2)+'\n')
    print(json.dumps({'build_id':data['build_id'],'source_sha256':source['sha256'],'files':len(files)}))


if __name__ == '__main__':
    main()
