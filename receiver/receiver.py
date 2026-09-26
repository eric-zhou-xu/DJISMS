#!/usr/local/bin/python3
"""Poll the bound modem every five seconds and archive locally without network access."""
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

def collect():
 locations=module_locations()
 if not locations:raise RuntimeError('未检测到模块 USB 连接')
 matched=[]
 for loc in locations:
  for _ in range(4):
   p=subprocess.run([str(Path(__file__).parent/'snapshot'),hex(loc)],capture_output=True,text=True,timeout=30)
   if p.returncode==15:time.sleep(1);continue
   break
  if p.returncode==0:matched.append(p)
 if len(matched)!=1:raise RuntimeError('绑定模块读取失败或身份不匹配；已保留原件')
 p=matched[0]
 block=p.stdout.split('QUERY AT+CMGL=4\n',1)[1];rows=[]
 for m in re.finditer(r'^\+CMGL:\s*(\d+),\d+,.*?,(\d+)\s*\n([0-9A-Fa-f]+)\s*$',block,re.M):
  pdu=m[3].upper();b=bytes.fromhex(pdu)
  if len(b)-1-b[0]!=int(m[2]):raise ValueError('PDU length mismatch')
  try:sender,body,multi=decode(pdu)
  except Exception:sender,body,multi='未知号码','[暂未解码，原始短信已保存]',False
  rows.append(dict(id=hashlib.sha256(pdu.encode()).hexdigest(),sender=sender,body=body,received_at=datetime.now(timezone.utc).isoformat(),multipart=multi,pdu=pdu))
 if block.count('+CMGL:')!=len(rows):raise ValueError('Incomplete SMS listing')
 return rows

def render(rows):
 lines=['DJISMS 短信收件箱','时间为保存时间（北京时间）；长短信可能分段显示。','']
 for d in sorted(rows,key=lambda x:x['received_at'],reverse=True):
  try:dt=datetime.fromisoformat(d['received_at'].replace('Z','+00:00')).astimezone().strftime('%Y-%m-%d %H:%M:%S')
  except ValueError:dt=d['received_at']
  lines.extend([dt+'  '+d.get('sender',''),d.get('body') or '[该历史记录正文未完整解码]','','────────────────────────',''])
 return '\n'.join(lines)+'\n'


import sqlite3,fcntl
ROOT=Path.home()/'DJISMS 短信'
def atomic(path,text):
 data=text.encode('utf-8')
 if path.exists() and path.read_bytes()==data:return
 tmp=path.with_name('.'+path.name+'.tmp')
 with open(tmp,'wb') as f:
  os.chmod(tmp,0o600);f.write(data);f.flush();os.fsync(f.fileno())
 os.replace(tmp,path)

def save_rows(db,rows):
 with db:
  for row in rows:db.execute('INSERT OR IGNORE INTO messages(id,data) VALUES (?,?)',(row['id'],json.dumps(row,ensure_ascii=False)))
 allrows=[json.loads(r[0]) for r in db.execute('SELECT data FROM messages')]
 atomic(ROOT/'收件箱.txt',render(allrows))
 return len(allrows)

def main():
 os.umask(0o077);ROOT.mkdir(mode=0o700,exist_ok=True)
 lock=open(ROOT/'.receiver.lock','w');fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
 db=sqlite3.connect(ROOT/'短信.sqlite3');db.execute('PRAGMA synchronous=FULL');db.execute('CREATE TABLE IF NOT EXISTS messages(id TEXT PRIMARY KEY,data TEXT NOT NULL)');db.commit()
 while True:
  start=time.monotonic()
  try:
   rows=collect();total=save_rows(db,rows)
   state={'ok':True,'saved_records':total,'module_parts':len(rows),'checked_at':datetime.now().astimezone().isoformat(),'scan_seconds':5}
  except Exception as e:
   state={'ok':False,'error':str(e) if isinstance(e,RuntimeError) else type(e).__name__,'checked_at':datetime.now().astimezone().isoformat(),'scan_seconds':5}
  atomic(ROOT/'运行状态.json',json.dumps(state,ensure_ascii=False,indent=2))
  if '--once' in sys.argv:break
  time.sleep(max(0,5-(time.monotonic()-start)))
if __name__=='__main__':main()
