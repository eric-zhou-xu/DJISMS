import fcntl, importlib.util, json, sqlite3, tempfile, unittest
from pathlib import Path

repo=Path(__file__).resolve().parents[1]
spec=importlib.util.spec_from_file_location('migration',repo/'migrate_archive.py')
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
spec=importlib.util.spec_from_file_location('receiver',repo/'receiver/receiver.py')
r=importlib.util.module_from_spec(spec);spec.loader.exec_module(r)

def archive(path, rows, deleted=()):
 path.mkdir(parents=True,exist_ok=True)
 with sqlite3.connect(path/'短信.sqlite3') as db:
  db.execute('CREATE TABLE messages(id TEXT PRIMARY KEY,data TEXT NOT NULL)')
  db.execute('CREATE TABLE local_deleted(id TEXT PRIMARY KEY)')
  for identity,pdu in rows:
   db.execute('INSERT INTO messages VALUES (?,?)',(identity,json.dumps(dict(id=identity,pdu=pdu,sender='test',body='example',received_at='2026-10-06T08:00:00+00:00'))))
  db.executemany('INSERT INTO local_deleted VALUES (?)',[(x,) for x in deleted])

class MigrationTest(unittest.TestCase):
 def test_merge_deduplicates_and_respects_deletions(self):
  with tempfile.TemporaryDirectory() as name:
   home=Path(name); old=home/'DJISMS 短信'; new=home/'Applications/DJISMS/DJISMS 短信'
   archive(old,[('a','AA'),('b','BB')],['c']);archive(new,[('a','AA'),('c','CC')],['b'])
   (old/'网络测试.json').write_text('{}')
   target=m.migrate_archive(home,r.render)
   with sqlite3.connect(target/'短信.sqlite3') as db:
    self.assertEqual(db.execute('SELECT id FROM messages').fetchall(),[('a',)])
    self.assertEqual(set(db.execute('SELECT id FROM local_deleted')), {('b',),('c',)})
   self.assertFalse(old.exists());self.assertTrue((new/'网络测试.json').exists())
   self.assertEqual((new/'收件箱.txt').read_text(),r.render([dict(id='a',pdu='AA',sender='test',body='example',received_at='2026-10-06T08:00:00+00:00')]))
   self.assertEqual(len(list((home/'Library/Application Support/DJISMS App Backups').glob('*.zip'))),2)
   self.assertEqual(m.migrate_archive(home,r.render),target)
 def test_conflicting_pdu_leaves_originals_intact(self):
  with tempfile.TemporaryDirectory() as name:
   home=Path(name); old=home/'DJISMS 短信'; new=home/'Applications/DJISMS/DJISMS 短信'
   archive(old,[('a','AA')]);archive(new,[('a','BB')])
   original=(new/'短信.sqlite3').read_bytes()
   with self.assertRaisesRegex(ValueError,'PDU'):m.migrate_archive(home,r.render)
   self.assertTrue(old.exists());self.assertEqual(original,(new/'短信.sqlite3').read_bytes())
 def test_running_receiver_blocks_migration(self):
  with tempfile.TemporaryDirectory() as name:
   home=Path(name); old=home/'DJISMS 短信';archive(old,[('a','AA')])
   with open(old/'.receiver.lock','a') as lock:
    fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
    with self.assertRaises(BlockingIOError):m.migrate_archive(home,r.render)
   self.assertTrue(old.exists())
 def test_corrupt_database_blocks_migration(self):
  with tempfile.TemporaryDirectory() as name:
   home=Path(name);old=home/'DJISMS 短信';old.mkdir();(old/'短信.sqlite3').write_bytes(b'broken')
   with self.assertRaises(sqlite3.DatabaseError):m.migrate_archive(home,r.render)
   self.assertTrue(old.exists())

if __name__=='__main__':unittest.main()
