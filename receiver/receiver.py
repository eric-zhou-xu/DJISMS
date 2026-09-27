#!/usr/local/bin/python3
"""Listen for bound-modem notifications and archive durably, then delete verified ME/SM records without network access."""
import sys,json,subprocess,time,hashlib,re,tempfile,os,plistlib
from pathlib import Path
from datetime import datetime,timezone
GSM='@£$¥èéùìòÇ\nØø\rÅåΔ_ΦΓΛΩΠΨΣΘΞ\x1bÆæßÉ !"#¤%&\'()*+,-./0123456789:;<=>?¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§¿abcdefghijklmnopqrstuvwxyzäöñüà'
EXT={10:'\f',20:'^',40:'{',41:'}',47:'\\',60:'[',61:'~',62:']',64:'|',101:'€'}
def gsm(data,count,start=0):
 value=int.from_bytes(data,'little');out='';esc=False
 for i in range(count):
  v=(value>>(start+7*i))&127
  if esc:out+=EXT.get(v,'�');esc=False
  elif v==27:esc=True
  else:out+=GSM[v]
 return out

def decode(pdu):
 b=bytes.fromhex(pdu);p=1+b[0];first=b[p];p+=1
 if first&3:raise ValueError('Unsupported SMS TPDU')
 n=b[p];typ=b[p+1];p+=2;address=b[p:p+(n+1)//2];p+=(n+1)//2
 sender=gsm(address,n*4//7) if typ&0x70==0x50 else ('+' if typ&0x70==0x10 else '')+''.join(f'{v&15:x}{v>>4:x}' for v in address)[:n]
 p+=1;dcs=b[p];p+=1;stamp=b[p:p+7];p+=7;udl=b[p];p+=1;data=b[p:];header=(data[0]+1) if first&64 else 0
 if header>len(data):raise ValueError('Invalid UDH')
 if dcs&32:raise ValueError('Compressed SMS unsupported')
 if dcs&12==8:
  if len(data)!=udl:raise ValueError('UCS2 length mismatch')
  body=data[header:].decode('utf-16-be')
 elif dcs&12==0:
  if len(data)!=(udl*7+7)//8:raise ValueError('GSM7 length mismatch')
  skip=(header*8+6)//7;body=gsm(data,udl-skip,skip*7)
 else:body='[二进制短信，原始数据已保存在本机]'
 return sender,body, bool(header)

def module_locations():
 tree=plistlib.loads(subprocess.check_output(['/usr/sbin/ioreg','-a','-p','IOUSB','-l'],timeout=10))
 found=set()
 def walk(node):
  if isinstance(node,dict):
   if node.get('idVendor')==0x2ca3 and node.get('idProduct')==0x4006 and node.get('locationID'):
    found.add(node['locationID'])
   for value in node.values():walk(value)
  elif isinstance(node,list):
   for value in node:walk(value)
 walk(tree)
 return sorted(found)

def parse_rows(output,store):
 block=output.split('QUERY AT+CMGL=4\n',1)[1];rows=[]
 for m in re.finditer(r'^\+CMGL:\s*(\d+),\d+,.*?,(\d+)\s*\n([0-9A-Fa-f]+)\s*$',block,re.M):
  pdu=m[3].upper();b=bytes.fromhex(pdu)
  if not b or len(b)-1-b[0]!=int(m[2]):raise ValueError('PDU length mismatch')
  try:sender,body,multi=decode(pdu)
  except Exception:sender,body,multi='未知号码','[暂未解码，原始短信已保存]',False
  rows.append(dict(storage=store,slot=int(m[1]),id=hashlib.sha256(pdu.encode()).hexdigest(),sender=sender,body=body,received_at=datetime.now(timezone.utc).isoformat(),multipart=multi,pdu=pdu))
 if block.count('+CMGL:')!=len(rows) or len({r['slot'] for r in rows})!=len(rows):raise ValueError('Incomplete or duplicate SMS listing')
 return rows

def collect():
 locations=module_locations()
 if not locations:raise RuntimeError('未检测到模块 USB 连接')
 matched=[]
 for loc in locations:
  for _ in range(4):
   p=subprocess.run([str(Path(__file__).parent/'snapshot'),hex(loc),'ME'],capture_output=True,text=True,timeout=150)
   if p.returncode==15:time.sleep(1);continue
   break
  if p.returncode==0:matched.append((loc,p))
 if len(matched)!=1:raise RuntimeError('绑定模块读取失败或身份不匹配；未清理原件')
 loc,p=matched[0];rows=parse_rows(p.stdout,'ME')
 storage_match=re.search(r'\+CPMS:\s*"([A-Z]+)",\d+,\d+,"([A-Z]+)",\d+,\d+,"([A-Z]+)"',p.stdout)
 storage=storage_match.groups() if storage_match else None
 sim=subprocess.run([str(Path(__file__).parent/'snapshot'),hex(loc),'SM'],capture_output=True,text=True,timeout=150)
 if sim.returncode:raise RuntimeError('SIM 存储检查失败；本轮未清理原件')
 rows+=parse_rows(sim.stdout,'SM')
 info={}
 for key,prefix in [('operator','COPS'),('signal','CSQ'),('lte','CEREG')]:
  match=re.search(r'^\+'+prefix+r':[^\r\n]*',p.stdout,re.M)
  if match:info[key]=match[0]
 for name,output in [('ME',p.stdout),('SM',sim.stdout)]:
  match=re.search(r'STORAGE '+name+r'\n.*?\+CPMS:\s*"'+name+r'",(\d+),(\d+)',output,re.S)
  if match:info[name]={'used':int(match[1]),'total':int(match[2])}
 return loc,rows,storage,info

def render(rows):
 lines=['DJISMS 短信收件箱','时间为保存时间（北京时间）；长短信可能分段显示。','']
 for d in sorted(rows,key=lambda x:x['received_at'],reverse=True):
  try:dt=datetime.fromisoformat(d['received_at'].replace('Z','+00:00')).astimezone().strftime('%Y-%m-%d %H:%M:%S')
  except ValueError:dt=d['received_at']
  lines.extend([dt+'  '+d.get('sender',''),d.get('body') or '[该历史记录正文未完整解码]','','────────────────────────',''])
 return '\n'.join(lines)+'\n'


import sqlite3,fcntl,select,signal
ROOT=Path.home()/'DJISMS 短信'
def atomic(path,text):
 data=text.encode('utf-8')
 if path.exists() and path.read_bytes()==data:return
 tmp=path.with_name('.'+path.name+'.tmp')
 with open(tmp,'wb') as f:
  os.chmod(tmp,0o600);f.write(data);f.flush();os.fsync(f.fileno())
 os.replace(tmp,path)

def archive_lock(fn):
 def wrapped(db,*args):
  with open(ROOT/'.archive-write.lock','a') as lock:
   fcntl.flock(lock,fcntl.LOCK_EX)
   db.execute('CREATE TABLE IF NOT EXISTS local_deleted(id TEXT PRIMARY KEY)');db.commit()
   return fn(db,*args)
 return wrapped

@archive_lock
def delete_local(db,message_id):
 if not isinstance(message_id,str) or not re.fullmatch('[0-9a-f]{64}',message_id):raise ValueError('Invalid message id')
 with db:
  db.execute('INSERT OR IGNORE INTO local_deleted(id) VALUES (?)',(message_id,))
  db.execute('DELETE FROM messages WHERE id=?',(message_id,))
 allrows=[json.loads(r[0]) for r in db.execute('SELECT data FROM messages')]
 atomic(ROOT/'收件箱.txt',render(allrows))
 return len(allrows)

@archive_lock
def save_rows(db,rows):
 with db:
  for row in rows:
   if db.execute('SELECT 1 FROM local_deleted WHERE id=?',(row['id'],)).fetchone():continue
   if hashlib.sha256(row['pdu'].encode()).hexdigest()!=row['id']:raise RuntimeError('短信身份校验失败')
   existing=db.execute('SELECT data FROM messages WHERE id=?',(row['id'],)).fetchone()
   if existing:
    old=json.loads(existing[0])
    if old.get('pdu') and old['pdu']!=row['pdu']:raise RuntimeError('本地原件不一致，拒绝覆盖')
    if not old.get('pdu'):
     enriched=dict(row);enriched['received_at']=old.get('received_at',row['received_at'])
     db.execute('UPDATE messages SET data=? WHERE id=?',(json.dumps(enriched,ensure_ascii=False),row['id']))
   else:db.execute('INSERT INTO messages(id,data) VALUES (?,?)',(row['id'],json.dumps(row,ensure_ascii=False)))
 for row in rows:
  if db.execute('SELECT 1 FROM local_deleted WHERE id=?',(row['id'],)).fetchone():continue
  saved=db.execute('SELECT data FROM messages WHERE id=?',(row['id'],)).fetchone()
  if not saved or json.loads(saved[0]).get('pdu')!=row['pdu']:raise RuntimeError('本地原始短信回读校验失败；未删除模块原件')
 allrows=[json.loads(r[0]) for r in db.execute('SELECT data FROM messages')]
 atomic(ROOT/'收件箱.txt',render(allrows))
 return len(allrows)

def delete_saved(db,location,rows):
 deleted=0
 for row in rows:
  if db.execute('SELECT 1 FROM local_deleted WHERE id=?',(row['id'],)).fetchone():continue
  if row.get('storage') not in ('ME','SM'):raise RuntimeError('未知短信存储，拒绝清理')
  saved=db.execute('SELECT data FROM messages WHERE id=?',(row['id'],)).fetchone()
  if not saved or json.loads(saved[0]).get('pdu')!=row['pdu']:raise RuntimeError('删除前本地校验失败；原件保留')
  with db:
   cursor=db.execute('INSERT INTO module_cleanup(message_id,slot,state) VALUES (?,?,?)',(row['id'],row['slot'],'pending'))
   receipt=cursor.lastrowid
  result=subprocess.run([str(Path(__file__).parent/'snapshot'),hex(location),'--delete',row['storage']],input=str(row['slot'])+' '+row['pdu']+'\n',capture_output=True,text=True,timeout=150)
  if result.returncode!=0 or result.stdout.strip()!='DELETED':
   raise RuntimeError('模块逐条清理未确认；本地已保存，剩余原件保留待重试')
  with db:db.execute("UPDATE module_cleanup SET state='confirmed' WHERE id=?",(receipt,))
  deleted+=1
 return deleted

def main():
 def shutdown(signum,frame):raise SystemExit(0)
 signal.signal(signal.SIGTERM,shutdown);signal.signal(signal.SIGINT,shutdown)
 os.umask(0o077);ROOT.mkdir(mode=0o700,exist_ok=True)
 lock=open(ROOT/'.receiver.lock','w');fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
 db=sqlite3.connect(ROOT/'短信.sqlite3');db.execute('PRAGMA synchronous=FULL');db.execute('CREATE TABLE IF NOT EXISTS messages(id TEXT PRIMARY KEY,data TEXT NOT NULL)');db.execute('CREATE TABLE IF NOT EXISTS module_cleanup(id INTEGER PRIMARY KEY,message_id TEXT NOT NULL,slot INTEGER NOT NULL,state TEXT NOT NULL)');db.commit()
 last_check=None
 recovery_attempt=0
 def publish(state):
  state.update(mode='notification',checked_at=datetime.now().astimezone().isoformat(),last_scan_at=last_check,cleanup_policy='verified_local_then_delete_me_sm',scan_seconds=None,app_version='1.3.3')
  atomic(ROOT/'运行状态.json',json.dumps(state,ensure_ascii=False,indent=2))
 request=ROOT/'.manual-check'
 while True:
  loc=None
  try:
   request.unlink(missing_ok=True)
   loc,rows,storage,info=collect();total=save_rows(db,rows);deleted=delete_saved(db,loc,rows);last_check=datetime.now().astimezone().isoformat()
   recovery_attempt=0
   state=dict(ok=True,saved_records=total,module_parts=len(rows),deleted_records=deleted,storage_configuration=storage,device_info=info,usb_location=hex(loc))
   publish(dict(state,listener='starting'))
   if '--once' in sys.argv:break
   child=subprocess.Popen([str(Path(__file__).parent/'snapshot'),hex(loc),'--watch'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL,text=True,bufsize=1)
   event=False;ready=False
   try:
    while child.poll() is None:
     if request.exists():child.stdin.write('CHECK\n');child.stdin.flush();request.unlink(missing_ok=True)
     readable,_,_=select.select([child.stdout],[],[],2)
     if readable:
      line=child.stdout.readline().strip()
      if line=='READY':ready=True
      elif line in ('EVENT','MANUAL'):event=True
     publish(dict(state,listener='listening' if ready else 'starting'))
    for line in child.stdout:
     if line.strip() in ('EVENT','MANUAL'):event=True
    if child.returncode!=0:raise RuntimeError('模块通知监听不可用，请点击立即补查')
    if not event:raise RuntimeError('通知监听已停止，请点击立即补查')
   finally:
    if child.poll() is None:
     child.terminate()
     try:child.wait(timeout=25)
     except subprocess.TimeoutExpired:child.kill();child.wait()
   continue
  except Exception as e:
   error=str(e) if isinstance(e,RuntimeError) else type(e).__name__
   if '--once' in sys.argv:
    publish(dict(ok=False,error=error,listener='manual_required'));break
   # Bounded recovery for enumeration/AT readiness; never periodic SMS polling.
   before=module_locations()
   delays=(1,3,6,10,15,30)
   delay=delays[recovery_attempt] if before and recovery_attempt<len(delays) else None
   if delay is not None:recovery_attempt+=1
   deadline=time.monotonic()+delay if delay is not None else None
   while True:
    publish(dict(ok=False,error=error,listener='reconnecting' if deadline is not None else 'manual_required',retry_attempt=recovery_attempt))
    time.sleep(1)
    now=module_locations()
    if request.exists() or now!=before:
     recovery_attempt=0;break
    if deadline is not None and time.monotonic()>=deadline:break
if __name__=='__main__':main()
