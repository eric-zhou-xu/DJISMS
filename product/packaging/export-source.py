#!/usr/bin/env python3
"""Create an isolated, minimal source snapshot; never change the source repo."""
import argparse, pathlib, shutil, subprocess, json, hashlib, os

p=argparse.ArgumentParser()
p.add_argument('destination')
p.add_argument('--go',default='go')
a=p.parse_args()
root=pathlib.Path(__file__).resolve().parents[2]
dest=pathlib.Path(a.destination).resolve()
if dest.exists(): raise SystemExit('Destination exists; refusing to overwrite it')
dest.mkdir(parents=True)
for name in ('product','internal/safeusb','internal/smsreceive','internal/smsarchive','pkg/smscodec','docs/djisms'):
    shutil.copytree(root/name,dest/name,ignore=shutil.ignore_patterns('.DS_Store','__pycache__'))
for name in ('LICENSE','go.sum'):
    shutil.copy2(root/name,dest/name)
# Preserve import identities while removing unrelated application dependencies.
# The only external module in the core + test dependency closure is pinned here.
(dest/'go.mod').write_text('module github.com/iniwex5/vohive\n\ngo 1.26.3\n\nrequire github.com/warthog618/sms v0.3.0\n')
(dest/'.gitignore').write_text('/build/\n.DS_Store\n__pycache__/\n*.test\n')
# Vendor the actual product dependency closure. No external dependency's own
# test-only modules need to be downloaded or shipped for this application.
(dest/'go.sum').write_text(''.join(line+'\n' for line in (root/'go.sum').read_text().splitlines() if line.startswith('github.com/warthog618/sms v0.3.0')))
env=dict(os.environ,GOPROXY='off',GOSUMDB='off')
subprocess.run([a.go,'mod','vendor'],cwd=dest,env=env,check=True)
closure=subprocess.check_output([a.go,'list','-deps','-test','-tags','djisms_native','-f','{{if not .Standard}}{{.ImportPath}}{{end}}','./product/djisms-core/...'],cwd=dest,env=env,text=True)
(dest/'DEPENDENCIES.txt').write_text(closure)
original={str(f.relative_to(root)):hashlib.sha256(f.read_bytes()).hexdigest() for name in ('product','internal/safeusb','internal/smsreceive','internal/smsarchive','pkg/smscodec') for f in (root/name).rglob('*') if f.is_file() and '__pycache__' not in f.parts and f.name!='.DS_Store'}
(dest/'SOURCE_ORIGIN.json').write_text(json.dumps({'module':'github.com/iniwex5/vohive','copied_file_hashes':original,'module_file_change':'Pruned unused dependencies; retained sms v0.3.0 and vendored its verified contents.'},indent=2)+'\n')
print(json.dumps({'source_directory':str(dest),'external_dependency':'github.com/warthog618/sms@v0.3.0','vendored':True}))
