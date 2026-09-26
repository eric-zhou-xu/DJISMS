from pathlib import Path
import subprocess,os,shutil,plistlib,datetime
base=Path(__file__).resolve().parent;src=base/'DJISMS.app';dest=Path('/Applications/DJISMS.app');home=Path.home();uid=str(os.getuid())
if not Path('/usr/local/bin/python3').exists():raise SystemExit('请先安装 Python 3 到 /usr/local/bin/python3')
if subprocess.run(['pgrep','-f','^/Applications/DJISMS.app/Contents/MacOS/DJISMS'],capture_output=True).returncode==0:raise SystemExit('请退出 DJISMS 界面后再安装，短信后台保持运行。')
if not src.is_dir():raise SystemExit('请从完整 DMG 中运行安装程序')
subprocess.run(['codesign','--verify','--deep','--strict',str(src)],check=True)
backup=home/'Library/Application Support/DJISMS App Backups';backup.mkdir(parents=True,exist_ok=True,mode=0o700)
if dest.exists():
 archive=backup/('DJISMS-'+datetime.datetime.now().strftime('%Y%m%d-%H%M%S')+'.zip')
 subprocess.run(['ditto','-c','-k','--keepParent',str(dest),str(archive)],check=True);archive.chmod(0o600)
local=home/'Library/Application Support/DJISMS Local';local.mkdir(parents=True,exist_ok=True,mode=0o700)
agents=home/'Library/LaunchAgents';agents.mkdir(parents=True,exist_ok=True)
for label in ['local.djisms.receiver','local.djisms.cloud']:subprocess.run(['launchctl','bootout','gui/'+uid+'/'+label],capture_output=True)
if dest.exists():shutil.rmtree(dest)
shutil.copytree(src,dest)
for n in ['receiver.py','snapshot']:shutil.copy2(dest/'Contents/Resources'/n,local/n)
for label,args,keep in [('local.djisms.receiver',['/usr/local/bin/python3',str(local/'receiver.py')],True),('local.djisms.cloud',['/usr/bin/open','-g',str(dest)],False)]:
 d={'Label':label,'ProgramArguments':args,'RunAtLoad':True}
 if keep:d.update(KeepAlive=True,ThrottleInterval=10,StandardErrorPath=str(local/'errors.log'))
 p=agents/(label+'.plist');p.write_bytes(plistlib.dumps(d));subprocess.run(['launchctl','bootstrap','gui/'+uid,str(p)],check=True)
print('安装完成。短信目录与历史记录保持不变。')
