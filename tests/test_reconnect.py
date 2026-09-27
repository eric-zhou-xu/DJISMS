import importlib.util,tempfile,unittest
from pathlib import Path
from unittest.mock import patch
SOURCE=Path(__file__).resolve().parents[1]/'receiver/receiver.py'
def load():
 s=importlib.util.spec_from_file_location('receiver_test',SOURCE);m=importlib.util.module_from_spec(s);s.loader.exec_module(m);return m
class ReconnectTest(unittest.TestCase):
 def run_case(self,success_at=None,move_at=None):
  m=load();clock=[0];calls=[]
  def collect():
   calls.append(clock[0])
   if success_at and len(calls)==success_at:raise SystemExit()
   raise RuntimeError('device not ready')
  def sleep(n):
   clock[0]+=n
   if clock[0]>150:raise SystemExit()
  with tempfile.TemporaryDirectory() as d:
   m.ROOT=Path(d)
   with patch.object(m,'collect',collect),patch.object(m,'module_locations',lambda:[2 if move_at and clock[0]>=move_at else 1]),patch.object(m.time,'sleep',sleep),patch.object(m.time,'monotonic',lambda:clock[0]),patch.object(m.signal,'signal'),patch.object(m.sys,'argv',['receiver.py']):
    with self.assertRaises(SystemExit):m.main()
  return calls
 def test_enumerated_but_not_ready_retries(self):
  self.assertEqual(len(self.run_case(success_at=3)),3)
 def test_retry_budget_stops_without_sms_polling(self):
  self.assertEqual(len(self.run_case()),7)
 def test_moving_usb_resets_recovery_budget(self):
  self.assertEqual(len(self.run_case(move_at=80)),14)
if __name__=='__main__':unittest.main()
