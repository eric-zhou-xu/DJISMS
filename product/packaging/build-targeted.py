#!/usr/bin/env python3
"""New, separately identified targeted Core/SM isolation fix. No historical build mutation."""
import json,os,pathlib,plistlib,shutil,subprocess,sys
import manifest
root=pathlib.Path(__file__).resolve().parents[2];base,out,go=map(pathlib.Path,sys.argv[1:4]);assert not out.exists()
source=manifest.sources(root);rev='20260923-targeted-isolation-01';version='1.0.1-fix.1';app=out/'DJISMS.app';shutil.copytree(base/'DJISMS.app',app)
env=os.environ.copy();env.update(CGO_ENABLED='1',GOOS='darwin',GOARCH='arm64',MACOSX_DEPLOYMENT_TARGET='14.0',CGO_CFLAGS='-mmacosx-version-min=14.0',CGO_LDFLAGS='-mmacosx-version-min=14.0')
def run(args):subprocess.run(list(map(str,args)),cwd=root,env=env,check=True)
pkg='github.com/iniwex5/vohive/product/djisms-core/buildinfo';flags=f'-s -w -X {pkg}.Version={version} -X {pkg}.BuildID={rev} -X {pkg}.SourceTree={source["sha256"]}'
for name in ['djisms-core','djisms-sim-store','djisms-cancel-recover']:
 dest=out/name if name=='djisms-cancel-recover' else app/'Contents/Helpers'/name
 run([go,'build','-trimpath','-buildvcs=false','-tags','djisms_native','-ldflags='+flags,'-o',dest,'./product/djisms-core/cmd/'+name]);run(['/usr/bin/codesign','--force','--sign','-','--options','runtime',dest])
run(['/usr/bin/xcrun','swiftc','-swift-version','5','-O','-target','arm64-apple-macos14.0','-framework','AppKit','-framework','UserNotifications','-framework','ServiceManagement',*sorted((root/'product/DJISMS.app/Sources').glob('*.swift')),'-o',app/'Contents/MacOS/DJISMS'])
p=app/'Contents/Info.plist';info=plistlib.loads(p.read_bytes());info.update(CFBundleVersion='1.4.3',DJISMSVersion=version,DJISMSBuildID=rev,DJISMSSourceTree=source['sha256'],DJISMSUIRevision=rev,DJISMSUISourceTree=source['sha256'],DJISMSSIMHelperRevision=rev,DJISMSSIMHelperSourceTree=source['sha256']);p.write_bytes(plistlib.dumps(info));shutil.copy(root/'product/DJISMS.app/Resources/USER_GUIDE.txt',app/'Contents/Resources/USER_GUIDE.txt')
run(['/usr/bin/codesign','--force','--sign','-','--options','runtime',app]);run(['/usr/bin/codesign','--verify','--deep','--strict',app]);assert manifest.sources(root)==source
m={'build_id':rev,'version':version,'bundle_build':'1.4.3','source':source,'git_commit':subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip(),'artifact_files':{p.relative_to(app).as_posix():manifest.digest(p) for p in sorted(app.rglob('*')) if p.is_file()},'engineering_recovery_sha256':manifest.digest(out/'djisms-cancel-recover'),'signature':'ad-hoc','notarized':False,'validation_scope':'targeted cancellation/isolation regression only; prior V1 remains frozen; no new Full Gate claim'}
(out/'release-manifest.json').write_text(json.dumps(m,ensure_ascii=False,indent=2)+'\n');print(rev,source['sha256'])
