"""Offline, backed-up migration of the legacy local SMS archive."""
from contextlib import ExitStack, closing
from datetime import datetime
from pathlib import Path
import fcntl, json, os, shutil, sqlite3, tempfile


def migrate_archive(home, render):
    home = Path(home)
    target = home / 'Applications/DJISMS/DJISMS 短信'
    legacy = home / 'DJISMS 短信'
    target.mkdir(parents=True, exist_ok=True, mode=0o700)
    target.chmod(0o700)
    if not legacy.exists() or legacy.resolve() == target.resolve():
        return target
    backups = home / 'Library/Application Support/DJISMS App Backups'
    backups.mkdir(parents=True, exist_ok=True, mode=0o700)
    stamp = datetime.now().strftime('%Y%m%d-%H%M%S-%f')
    with ExitStack() as stack:
        for directory in (target, legacy):
            lock = stack.enter_context(open(directory / '.receiver.lock', 'a'))
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        for label, directory in (('Current SMS', target), ('Legacy SMS', legacy)):
            archive = shutil.make_archive(str(backups / f'{label}-{stamp}'), 'zip', directory)
            Path(archive).chmod(0o600)
        with tempfile.TemporaryDirectory(prefix='.migration-', dir=target) as temporary:
            staged = Path(temporary) / '短信.sqlite3'
            current = target / '短信.sqlite3'
            with closing(sqlite3.connect(staged)) as out:
                if current.exists():
                    with closing(sqlite3.connect(f'file:{current}?mode=ro', uri=True)) as original:
                        if original.execute('PRAGMA integrity_check').fetchone()[0] != 'ok':
                            raise ValueError('目标数据库损坏，保留原件与备份')
                        original.backup(out)
                out.execute('CREATE TABLE IF NOT EXISTS messages(id TEXT PRIMARY KEY,data TEXT NOT NULL)')
                out.execute('CREATE TABLE IF NOT EXISTS local_deleted(id TEXT PRIMARY KEY)')
                out.execute('CREATE TABLE IF NOT EXISTS module_cleanup(id INTEGER PRIMARY KEY,message_id TEXT NOT NULL,slot INTEGER NOT NULL,state TEXT NOT NULL)')
                old = legacy / '短信.sqlite3'
                if old.exists():
                    with closing(sqlite3.connect(f'file:{old}?mode=ro', uri=True)) as incoming:
                        if incoming.execute('PRAGMA integrity_check').fetchone()[0] != 'ok':
                            raise ValueError('旧数据库损坏，保留原件与备份')
                        tables = {r[0] for r in incoming.execute("SELECT name FROM sqlite_master WHERE type='table'")}
                        if tables - {'messages', 'local_deleted', 'module_cleanup', 'sqlite_sequence'}:
                            raise ValueError('旧档案包含未知数据表，停止迁移')
                        if 'local_deleted' in tables:
                            out.executemany('INSERT OR IGNORE INTO local_deleted(id) VALUES (?)', incoming.execute('SELECT id FROM local_deleted'))
                        if 'messages' in tables:
                            for identity, encoded in incoming.execute('SELECT id,data FROM messages'):
                                row = json.loads(encoded)
                                existing = out.execute('SELECT data FROM messages WHERE id=?', (identity,)).fetchone()
                                if existing:
                                    prior = json.loads(existing[0])
                                    if prior.get('pdu') and row.get('pdu') and prior['pdu'] != row['pdu']:
                                        raise ValueError('原始 PDU 冲突，未覆盖原档案')
                                    if prior.get('pdu') or not row.get('pdu'):
                                        continue
                                    row['received_at'] = prior.get('received_at', row.get('received_at'))
                                    encoded = json.dumps(row, ensure_ascii=False)
                                out.execute('INSERT OR REPLACE INTO messages(id,data) VALUES (?,?)', (identity, encoded))
                        if 'module_cleanup' in tables:
                            for record in incoming.execute('SELECT message_id,slot,state FROM module_cleanup'):
                                if not out.execute('SELECT 1 FROM module_cleanup WHERE message_id=? AND slot=? AND state=?', record).fetchone():
                                    out.execute('INSERT INTO module_cleanup(message_id,slot,state) VALUES (?,?,?)', record)
                out.execute('DELETE FROM messages WHERE id IN (SELECT id FROM local_deleted)')
                out.commit()
                if out.execute('PRAGMA integrity_check').fetchone()[0] != 'ok':
                    raise ValueError('合并数据库未通过完整性检查')
                rows = [json.loads(r[0]) for r in out.execute('SELECT data FROM messages')]
                (Path(temporary) / '收件箱.txt').write_text(render(rows), encoding='utf-8')
            if current.exists():
                with closing(sqlite3.connect(current)) as previous:
                    previous.execute('PRAGMA wal_checkpoint(TRUNCATE)')
            for name in ('短信.sqlite3', '收件箱.txt'):
                artifact = Path(temporary) / name
                artifact.chmod(0o600)
                with open(artifact, 'rb') as handle:
                    os.fsync(handle.fileno())
                os.replace(artifact, target / name)
            for entry in legacy.iterdir():
                if entry.is_file() and not entry.name.startswith('.') and not entry.name.startswith('短信.sqlite3') and entry.name != '收件箱.txt' and not (target / entry.name).exists():
                    shutil.copy2(entry, target / entry.name)
                    (target / entry.name).chmod(0o600)
            legacy.rename(backups / f'Legacy SMS directory-{stamp}')
    return target
