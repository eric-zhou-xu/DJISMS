import importlib.util,json,sqlite3,tempfile,hashlib,unittest,threading,time
from pathlib import Path
from unittest.mock import patch
spec=importlib.util.spec_from_file_location('receiver',Path(__file__).parents[1]/'receiver/receiver.py');m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
class ClearHistory(unittest.TestCase):
 def setUp(self):
  self.tmp=tempfile.TemporaryDirectory();self.root=Path(self.tmp.name);self.old=(m.ROOT,m.UI_ROOT,m.NOTIFICATION_PREFS);m.ROOT=self.root;m.UI_ROOT=self.root/'ui';m.UI_ROOT.mkdir();m.NOTIFICATION_PREFS=self.root/'notifications.json';self.db=sqlite3.connect(self.root/'短信.sqlite3');self.db.execute('CREATE TABLE messages(id TEXT PRIMARY KEY,data TEXT NOT NULL)');self.db.execute('CREATE TABLE module_cleanup(id INTEGER PRIMARY KEY,message_id TEXT,slot INTEGER,state TEXT)');self.ids=[]
  for i in range(3):
   row=self.row(str(i));self.ids.append(row['id']);self.db.execute('INSERT INTO messages VALUES (?,?)',(row['id'],json.dumps(row)))
  self.db.commit();m.atomic(self.root/'收件箱.txt','fake secret text')
  (m.UI_ROOT/'presentation.json').write_text(json.dumps({'read':self.ids,'known':self.ids,'stamps':{i:{'number':'fake'} for i in self.ids},'numbers':{'sim':'keep'},'appearance':'dark','sound':False}))
  m.NOTIFICATION_PREFS.write_text(json.dumps({'seen':self.ids,'notifications':False}))
 def row(self,value):
  return dict(id=hashlib.sha256(value.encode()).hexdigest(),pdu=value,body='fake secret '+value,received_at='2026-10-09T00:00:00Z',sender='fake')
 def tearDown(self):self.db.close();m.ROOT,m.UI_ROOT,m.NOTIFICATION_PREFS=self.old;self.tmp.cleanup()
 def clear(self,ids=None):return m.clear_history(self.db,self.ids if ids is None else ids,'a'*32)
 def test_clear_all_and_preferences(self):
  r=self.clear();self.assertEqual(r['deleted'],3);self.assertEqual(r['remaining'],0);self.assertNotIn('secret',(self.root/'收件箱.txt').read_text());self.assertNotIn(b'fake secret',(self.root/'短信.sqlite3').read_bytes());v=json.loads((m.UI_ROOT/'presentation.json').read_text());self.assertEqual(v['stamps'],{});self.assertEqual(v['numbers'],{'sim':'keep'});self.assertFalse(v['sound']);self.assertFalse(json.loads(m.NOTIFICATION_PREFS.read_text())['notifications'])
 def test_repeated_and_empty(self):
  self.assertEqual(self.clear(),self.clear());self.assertEqual(m.clear_history(self.db,[],'b'*32)['deleted'],0)
 def test_restart_and_reimport(self):
  self.clear();self.db.close();self.db=sqlite3.connect(self.root/'短信.sqlite3');m.retry_clear(self.db);m.save_rows(self.db,[self.row('0')]);self.assertEqual(self.db.execute('select count(*) from messages').fetchone()[0],0)
 def test_new_arrival_preserved(self):
  r=self.row('new');m.save_rows(self.db,[r]);result=self.clear();self.assertEqual(result['remaining'],1);self.assertIn('fake secret new',(self.root/'收件箱.txt').read_text())
 def test_bad_preferences_prevents_delete(self):
  (m.UI_ROOT/'presentation.json').write_text('bad')
  with self.assertRaises(ValueError):self.clear()
  self.assertEqual(self.db.execute('select count(*) from messages').fetchone()[0],3)
 def test_partial_failure_recovery(self):
  real=m.atomic
  def fail(path,text):
   if path.name=='收件箱.txt':raise OSError('injected disk error')
   real(path,text)
  with patch.object(m,'atomic',side_effect=fail):
   with self.assertRaises(OSError):self.clear()
  self.assertTrue((self.root/'.clear-history.json').exists());self.db.close();self.db=sqlite3.connect(self.root/'短信.sqlite3');m.retry_clear(self.db);self.assertFalse((self.root/'.clear-history.json').exists());self.assertNotIn('secret',(self.root/'收件箱.txt').read_text());self.assertEqual(self.clear()['deleted'],3)
 def test_cancellation_is_preview_only(self):
  before=(self.root/'短信.sqlite3').read_bytes();list(self.db.execute('select id from messages'));self.assertEqual(before,(self.root/'短信.sqlite3').read_bytes())
 def test_receiver_waits_for_clear_lock(self):
  import fcntl
  lock=open(self.root/'.archive-write.lock','a');fcntl.flock(lock,fcntl.LOCK_EX);done=threading.Event()
  def worker():
   with sqlite3.connect(self.root/'短信.sqlite3') as db:m.save_rows(db,[self.row('new')]);done.set()
  t=threading.Thread(target=worker);t.start();self.assertFalse(done.wait(.1));fcntl.flock(lock,fcntl.LOCK_UN);lock.close();t.join(2);self.assertTrue(done.is_set())
if __name__=='__main__':unittest.main()
