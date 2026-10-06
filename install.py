from pathlib import Path
import datetime, importlib.util, os, plistlib, shutil, subprocess, sys
sys.dont_write_bytecode = True
from migrate_archive import migrate_archive


def main():
    os.umask(0o077)
    base = Path(__file__).resolve().parent
    src = base / 'DJISMS.app'
    home = Path.home()
    folder = home / '个人程序/DJISMS'
    dest = folder / 'DJISMS.app'
    uid = str(os.getuid())
    if not Path('/usr/local/bin/python3').exists():
        raise SystemExit('请先安装 Python 3 到 /usr/local/bin/python3')
    if subprocess.run(['pgrep', '-f', r'/DJISMS\.app/Contents/MacOS/DJISMS'], capture_output=True).returncode == 0:
        raise SystemExit('请退出 DJISMS 界面后再安装。安装期间后台接收会短暂暂停。')
    if not src.is_dir():
        raise SystemExit('请从完整安装包中运行安装程序')
    subprocess.run(['codesign', '--verify', '--deep', '--strict', str(src)], check=True)
    folder.mkdir(parents=True, exist_ok=True)
    backup = home / 'Library/Application Support/DJISMS App Backups'
    backup.mkdir(parents=True, exist_ok=True, mode=0o700)
    stamp = datetime.datetime.now().strftime('%Y%m%d-%H%M%S-%f')
    if dest.exists():
        archive = backup / ('DJISMS-' + stamp + '.zip')
        subprocess.run(['ditto', '-c', '-k', '--keepParent', str(dest), str(archive)], check=True)
        archive.chmod(0o600)
    local = home / 'Library/Application Support/DJISMS Local'
    local.mkdir(parents=True, exist_ok=True, mode=0o700)
    agents = home / 'Library/LaunchAgents'
    agents.mkdir(parents=True, exist_ok=True)
    for label in ['local.djisms.receiver', 'local.djisms.cloud']:
        subprocess.run(['launchctl', 'bootout', 'gui/' + uid + '/' + label], capture_output=True)
    spec = importlib.util.spec_from_file_location('djisms_archive', src / 'Contents/Resources/receiver.py')
    receiver = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(receiver)
    migrate_archive(home, receiver.render)
    if dest.exists():
        dest.rename(backup / ('DJISMS previous-' + stamp + '.app'))
    shutil.copytree(src, dest)
    for name in ['receiver.py', 'snapshot']:
        shutil.copy2(dest / 'Contents/Resources' / name, local / name)
    for label, args, keep in [
        ('local.djisms.receiver', ['/usr/local/bin/python3', str(local / 'receiver.py')], True),
        ('local.djisms.cloud', ['/usr/bin/open', '-g', str(dest)], False),
    ]:
        config = {'Label': label, 'ProgramArguments': args, 'RunAtLoad': True}
        if keep:
            config.update(KeepAlive=True, ThrottleInterval=10, StandardErrorPath=str(local / 'errors.log'))
        path = agents / (label + '.plist')
        path.write_bytes(plistlib.dumps(config))
        subprocess.run(['launchctl', 'bootstrap', 'gui/' + uid, str(path)], check=True)
    print('安装完成：' + str(folder) + '。应用与短信档案已统一收纳；旧档案已在本机备份。')


if __name__ == '__main__':
    main()
