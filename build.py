from pathlib import Path
import subprocess,shutil,sys
src=Path(__file__).resolve().parent;out=Path(sys.argv[1]).resolve()
if out.exists():raise SystemExit('Output already exists')
r=out/'Contents/Resources';m=out/'Contents/MacOS';r.mkdir(parents=True);m.mkdir()
shutil.copy2(src/'Info.plist',out/'Contents/Info.plist')
for n in ['LICENSE.txt','SMS_LIBRARY_LICENSE.txt','THIRD_PARTY_LICENSE.txt','AppIcon.icns','AppMark.png']:shutil.copy2(src/n,r/n)
for n in ['local_bridge.py']:shutil.copy2(src/'ui'/n,r/n)
for n in ['receiver.py']:shutil.copy2(src/'receiver'/n,r/n)
subprocess.run(['swiftc',*[str(src/'ui'/n) for n in ['Models.swift','CoreClient.swift','SIMStorage.swift','main.swift']],'-o',str(m/'DJISMS'),'-framework','AppKit','-framework','UserNotifications','-framework','ServiceManagement'],check=True)
subprocess.run(['clang','-Wno-deprecated-declarations',str(src/'receiver/snapshot.c'),'-framework','IOKit','-framework','CoreFoundation','-o',str(r/'snapshot')],check=True)
subprocess.run(['codesign','--force','--deep','--sign','-',str(out)],check=True)
subprocess.run(['codesign','--verify','--deep','--strict',str(out)],check=True)
