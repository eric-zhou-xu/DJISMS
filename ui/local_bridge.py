"""Local archive presentation and deletion bridge; USB ownership remains with local.djisms.receiver."""
import json,sys,sqlite3,time,os
from pathlib import Path
from datetime import datetime
os.umask(0o077)
root=Path.home()/'DJISMS 短信'
prefs_path=Path.home()/'Library/Application Support/DJISMS Local/ui-notifications.json'
try:prefs=json.loads(prefs_path.read_text())
except (FileNotFoundError,ValueError):prefs={'notifications':True,'seen':[]}
seen=set(prefs.get('seen',[]));initial=True;last_ids=None

def emit(v):print(json.dumps(dict(version=1,**v),ensure_ascii=False),flush=True)
def messages():
 with sqlite3.connect('file:'+str(root/'短信.sqlite3')+'?mode=ro',uri=True) as db:
  rows=[json.loads(r[0]) for r in db.execute('select data from messages')]
 return sorted([{'id':r['id'],'sender':r.get('sender',''),'body':r.get('body') or '[历史记录正文缺失，原件待核对]','created_at':r['received_at'],'sent_at':None,'state':'complete' if r.get('body') else 'incomplete','notification_state':'submitted' if r['id'] in seen else 'pending','parts':1,'sim_id':None,'sim_number':None} for r in rows],key=lambda r:r['created_at'],reverse=True)
def save():
 prefs['seen']=sorted(seen);tmp=prefs_path.with_suffix('.tmp');tmp.write_text(json.dumps(prefs));tmp.chmod(0o600);tmp.replace(prefs_path)
emit({'event':'hello'})
for line in sys.stdin:
 try:
  q=json.loads(line);method=q.get('method');data={}
  if method=='status':
   s=json.loads((root/'运行状态.json').read_text());fresh=time.time()-(root/'运行状态.json').stat().st_mtime<20;ok=s.get('ok') and fresh
   data={'state':{'phase':'ready' if ok else 'disconnected','connected':bool(ok),'sim':'READY' if ok else '', 'status_observed':s.get('checked_at',''),'ui_failure':('模块通知接收 · 保存校验后清理原件' if s.get('listener')=='listening' else '正在连接通知监听') if ok else ('USB 重连中，请稍候' if s.get('listener')=='reconnecting' else s.get('error','后台状态过期，待恢复')),'internet':'未单独核验'},'preferences':{'notifications':prefs.get('notifications',True),'auto_purge':True}}
   rows=messages();ids={r['id'] for r in rows}
   if initial:seen.update(ids);save();initial=False
   if last_ids is not None and ids!=last_ids:emit({'event':'history_changed'})
   last_ids=ids
  elif method=='delete_local':
   import importlib.util
   spec=importlib.util.spec_from_file_location('djisms_archive',Path(__file__).with_name('receiver.py'));module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
   with sqlite3.connect(root/'短信.sqlite3',timeout=30) as db:
    db.execute('PRAGMA synchronous=FULL');remaining=module.delete_local(db,q.get('message_id'))
   data={'deleted':True,'remaining':remaining};emit({'event':'history_changed'})
  elif method=='rescan':
   path=root/'.manual-check';path.touch(mode=0o600);data={'requested':True}
  elif method=='messages':
   rows=messages();search=str(q.get('search','')).casefold();rows=[r for r in rows if search in (r['sender']+' '+r['body']).casefold()];offset=max(0,int(q.get('offset',0)));limit=min(200,max(1,int(q.get('limit',200))));data=rows[offset:offset+limit]
  elif method=='message':data=next(r for r in messages() if r['id']==q.get('message_id'))
  elif method=='notifications':data=[] if initial else [r for r in messages() if r['id'] not in seen]
  elif method=='notification_result':seen.add(q['message_id']);save()
  elif method=='preferences':prefs['notifications']=bool(q.get('preferences',{}).get('notifications',True));save()
  elif method=='shutdown':emit({'id':q.get('id'),'data':{}});break
  elif method=='power':pass
  else:raise ValueError('unsupported request')
  emit({'id':q.get('id'),'data':data})
 except Exception:emit({'id':q.get('id') if isinstance(q,dict) else None,'error':'本地短信读取暂不可用；原件未改动'})
