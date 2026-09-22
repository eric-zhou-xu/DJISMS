#!/usr/bin/env python3
"""Build the separate SM companion/UI; never rebuild or re-sign the accepted Core."""
import hashlib, json, os, pathlib, plistlib, shutil, subprocess, sys
import manifest
root=pathlib.Path(__file__).resolve().parents[2]
base, out, go=map(pathlib.Path,sys.argv[1:4])
if out.exists(): raise SystemExit('Refuse to overwrite an existing release directory')
expected='d04ff13ccd69b18c4bb2d916f9ef791a0e708acce71826d2821e36aa2d5577f0'
core=base/'DJISMS.app/Contents/Helpers/djisms-core'
assert manifest.digest(core)==expected
source=manifest.sources(root)
revision='20260922-sim-storage-01'
app=out/'DJISMS.app';shutil.copytree(base/'DJISMS.app',app)
env=os.environ.copy();env.update(CGO_ENABLED='1',GOOS='darwin',GOARCH='arm64',MACOSX_DEPLOYMENT_TARGET='14.0',CGO_CFLAGS='-mmacosx-version-min=14.0',CGO_LDFLAGS='-mmacosx-version-min=14.0')
def run(args): subprocess.run([str(x) for x in args],cwd=root,env=env,check=True)
helper=app/'Contents/Helpers/djisms-sim-store'
run([go,'build','-trimpath','-buildvcs=false','-tags','djisms_native','-ldflags=-s -w','-o',helper,'./product/djisms-core/cmd/djisms-sim-store'])
run(['/usr/bin/xcrun','swiftc','-swift-version','5','-O','-target','arm64-apple-macos14.0','-framework','AppKit','-framework','UserNotifications','-framework','ServiceManagement',*sorted((root/'product/DJISMS.app/Sources').glob('*.swift')),'-o',app/'Contents/MacOS/DJISMS'])
shutil.copy(root/'product/DJISMS.app/Resources/USER_GUIDE.txt',app/'Contents/Resources/USER_GUIDE.txt')
p=app/'Contents/Info.plist';info=plistlib.loads(p.read_bytes());info.update(CFBundleVersion='1.4.0',DJISMSUIRevision=revision,DJISMSUISourceTree=source['sha256'],DJISMSSIMHelperRevision=revision,DJISMSSIMHelperSourceTree=source['sha256']);p.write_bytes(plistlib.dumps(info))
run(['/usr/bin/codesign','--force','--sign','-','--options','runtime',helper])
run(['/usr/bin/codesign','--force','--sign','-','--options','runtime',app])
run(['/usr/bin/codesign','--verify','--deep','--strict','--verbose=2',app])
assert manifest.digest(app/'Contents/Helpers/djisms-core')==expected
assert manifest.sources(root)==source
result={'feature_revision':revision,'bundle_build':'1.4.0','git_commit':subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip(),'source':source,'accepted_core_candidate':'20260922-v1-final-r8-01','accepted_core_sha256':expected,'accepted_core_unchanged':True,'signature':'ad-hoc','notarized':False,'artifact_files':{p.relative_to(app).as_posix():manifest.digest(p) for p in sorted(app.rglob('*')) if p.is_file()},'validation_scope':'SM storage feature only; no new V1 Physical Gate claim'}
(out/'release-manifest.json').write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({k:v for k,v in result.items() if k not in ('source','artifact_files')}))
