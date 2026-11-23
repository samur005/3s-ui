import json, urllib.request, urllib.parse, time
def tok(n): return open(n+'.tok').read().strip()
def call(port, t, action, method='GET', form=None, q=None):
    url=f'http://127.0.0.1:{port}/app/apiv2/'+action+('?'+urllib.parse.urlencode(q) if q else '')
    data=urllib.parse.urlencode(form).encode() if form is not None else None
    req=urllib.request.Request(url, data=data, method=method); req.add_header('Token', t)
    if data: req.add_header('Content-Type','application/x-www-form-urlencoded')
    with urllib.request.urlopen(req, timeout=60) as r: return json.loads(r.read() or b'null')
res=[]
def check(name, cond, info=''):
    res.append(cond); print(('PASS' if cond else 'FAIL'), name, '' if cond else info)
M,MT=23095,tok('master')
# new master -> legacy node (main branch)
n={'name':'legacy-node','baseUrl':'http://127.0.0.1:25095','webPath':'/app/','token':tok('legacy'),'enable':True,'allowPrivate':True,'syncMode':'one_way'}
check('new master: add legacy node', call(M,MT,'save','POST',{'object':'nodes','action':'new','data':json.dumps(n)})['success'])
lid=[x for x in call(M,MT,'load')['obj']['nodes'] if x['name']=='legacy-node'][0]['id']
r=call(M,MT,'nodeRemote','POST',{'id':lid,'op':'inbounds'}); check('new master -> legacy: inbounds (legacy op)', r['success'], r)
r=call(M,MT,'nodeRemote','POST',{'id':lid,'op':'health'}); check('new master -> legacy: health falls back to status', r['success'] and r['obj'].get('legacy') is True, r)
r=call(M,MT,'nodeRemote','POST',{'id':lid,'op':'checkConfig','data':'{}'}); check('new master -> legacy: v2-only op refused with upgrade hint', not r['success'] and 'older 3S-UI' in r['msg'], r)
r=call(M,MT,'nodeTokenRotate','POST',{'id':lid}); check('new master -> legacy: token rotation refused', not r['success'], r)
ib={'type':'vless','tag':'legacy-vless','listen':'::','listen_port':25443,'users':[],'tls':{}}
r=call(M,MT,'nodeRemote','POST',{'id':lid,'op':'save.inbounds','action':'new','data':json.dumps(ib)}); check('new master -> legacy: create inbound (legacy save)', r['success'], r)
tags=[x['tag'] for x in call(25095,tok('legacy'),'inbounds')['obj']['inbounds']]; check('legacy node has the inbound', 'legacy-vless' in tags, tags)
call(M,MT,'nodeRemote','POST',{'id':lid,'op':'save.inbounds','action':'del','data':json.dumps('legacy-vless')})
time.sleep(7)
d=call(M,MT,'nodeDetail',q={'id':lid}); st=d['obj']['status']
check('new master: legacy node online, apiVersion 1, not incompatible', st.get('state')=='online' and st.get('apiVersion')==1 and st.get('health') in ('online','degraded'), st)
# old master (main) -> new node
O,OT=26095,tok('oldmaster')
n2={'name':'new-node','baseUrl':'http://127.0.0.1:22095','webPath':'/app/','token':open('node.tok').read().strip(),'enable':True,'syncMode':'one_way'}
# the node's token was rotated in e2e.py; read the current one from the new master's DB
import sqlite3
n2['token']=sqlite3.connect('master/3s-ui.db').execute("select token from nodes where name='local-node'").fetchone()[0]
check('old master: add new node', call(O,OT,'save','POST',{'object':'nodes','action':'new','data':json.dumps(n2)})['success'])
time.sleep(7)
st=call(O,OT,'load')['obj'].get('nodesStatus',{})
s=list(st.values())[0] if st else {}
check('old master -> new node: heartbeat online', s.get('state')=='online', st)
print('SUMMARY %d/%d' % (sum(res), len(res)))
