import json, urllib.request, urllib.parse, sys, time
NT=open('node.tok').read().strip(); MT=open('master.tok').read().strip()
NODE='http://127.0.0.1:22095/app/apiv2/'; MASTER='http://127.0.0.1:23095/app/apiv2/'
res=[]
def call(base, tok, action, method='GET', form=None, q=None, headers=None):
    url=base+action+('?'+urllib.parse.urlencode(q) if q else '')
    data=urllib.parse.urlencode(form).encode() if form is not None else None
    req=urllib.request.Request(url, data=data, method=method)
    req.add_header('Token', tok)
    for k,v in (headers or {}).items(): req.add_header(k,v)
    if data: req.add_header('Content-Type','application/x-www-form-urlencoded')
    with urllib.request.urlopen(req, timeout=60) as r:
        return json.loads(r.read() or b'null'), dict(r.headers)
def check(name, cond, info=''):
    res.append((name, bool(cond))); print(('PASS' if cond else 'FAIL'), name, '' if cond else info)

# node side v2 API
h,_=call(NODE,NT,'nodeHello'); check('node: nodeHello apiVersion=2', h['success'] and h['obj']['apiVersion']==2, h)
c,_=call(NODE,NT,'nodeCapabilities'); caps=c['obj']
check('node: capabilities list vless/hysteria2 from registry', 'vless' in caps['inbounds'] and 'hysteria2' in caps['inbounds'], caps.get('inbounds'))
check('node: capabilities qwdtt panel type present', 'qwdtt' in (caps.get('panelInbounds') or []), caps.get('panelInbounds'))
hh,_=call(NODE,NT,'nodeHealth'); check('node: nodeHealth', hh['success'] and 'cpu' in hh['obj'], hh)
bad,_=call(NODE,'wrong','nodeHello'); check('node: wrong token refused', not bad['success'])
# replay protection
old=str(int(time.time())-3600)
r,_=call(NODE,NT,'restartSb','POST',{},headers={'X-Request-Timestamp':old}); check('node: stale X-Request-Timestamp refused', not r['success'], r)

# master: add node via apiv2 save (loopback needs allowPrivate)
node={'name':'local-node','baseUrl':'http://127.0.0.1:22095','webPath':'/app/','token':NT,'enable':True,'insecure':False,'syncMode':'one_way','allowPrivate':False,'group':'eu','location':'Tallinn'}
r,_=call(MASTER,MT,'save','POST',{'object':'nodes','action':'new','data':json.dumps(node)})
check('master: add node saved', r['success'], r)
nodes,_=call(MASTER,MT,'load')
lst=nodes['obj']['nodes']
n=[x for x in lst if x['name']=='local-node'][0]; nid=n['id']
check('master: token never returned', 'token' not in n or not n.get('token'), n)
check('master: group/location stored', n.get('group')=='eu' and n.get('location')=='Tallinn', n)
r,_=call(MASTER,MT,'nodeRemote','POST',{'id':nid,'op':'hello'})
check('master: SSRF guard refuses loopback node without allowPrivate', not r['success'] and 'SSRF' in r['msg'], r)
# metadata address rejected even with allowPrivate
n2=dict(node, name='meta', baseUrl='http://169.254.169.254', allowPrivate=True)
r,_=call(MASTER,MT,'save','POST',{'object':'nodes','action':'new','data':json.dumps(n2)})
nodes,_=call(MASTER,MT,'load'); lst=nodes['obj']['nodes']
meta=[x for x in lst if x['name']=='meta']
if meta:
    r2,_=call(MASTER,MT,'nodeRemote','POST',{'id':meta[0]['id'],'op':'hello'})
    check('master: link-local metadata address always refused', not r2['success'] and 'SSRF' in r2['msg'], r2)
    call(MASTER,MT,'save','POST',{'object':'nodes','action':'del','data':json.dumps(meta[0]['id'])})
else:
    check('master: metadata node refused at save', not r['success'], r)
# enable allowPrivate
n['allowPrivate']=True; n['token']=''
r,_=call(MASTER,MT,'save','POST',{'object':'nodes','action':'edit','data':json.dumps(n)})
check('master: edit node (keep token)', r['success'], r)
r,_=call(MASTER,MT,'nodeRemote','POST',{'id':nid,'op':'hello'})
check('master->node: hello via typed remote op', r['success'] and r['obj']['apiVersion']==2, r)
r,_=call(MASTER,MT,'nodeRemote','POST',{'id':nid,'op':'capabilities'})
check('master->node: capabilities', r['success'] and 'vless' in r['obj']['inbounds'], r.get('msg'))
# create inbound remotely
ib={'type':'vless','tag':'e2e-vless','listen':'::','listen_port':24443,'users':[],'tls':{}}
r,_=call(MASTER,MT,'nodeRemote','POST',{'id':nid,'op':'save.inbounds','action':'new','data':json.dumps(ib)})
check('master->node: create vless inbound', r['success'], r)
ni,_=call(NODE,NT,'inbounds'); tags=[x['tag'] for x in ni['obj']['inbounds']]
check('node: inbound exists after remote create', 'e2e-vless' in tags, tags)
r,_=call(MASTER,MT,'nodeRemote','POST',{'id':nid,'op':'save.inbounds','action':'new','data':json.dumps(dict(ib,type='nonexistent',tag='x'))})
check('master: unsupported inbound type refused before node', not r['success'] and 'does not support' in r['msg'], r)
r,_=call(MASTER,MT,'nodeRemote','POST',{'id':nid,'op':'portCheck','params':json.dumps({'port':'24443','network':'tcp'})})
check('master->node: portCheck sees used port', r['success'], r)
print('   portCheck:', r.get('obj'))
# route section
cfg,_=call(MASTER,MT,'nodeRemote','POST',{'id':nid,'op':'config'})
route=cfg['obj']['config'].get('route',{}); route['final']='direct'
r,_=call(MASTER,MT,'nodeRemote','POST',{'id':nid,'op':'config.route','data':json.dumps(route)})
check('master->node: save route section', r['success'], r)
nc,_=call(NODE,NT,'config'); check('node: route.final updated', nc['obj']['config']['route'].get('final')=='direct', nc['obj']['config'].get('route'))
r,_=call(MASTER,MT,'nodeRemote','POST',{'id':nid,'op':'config.dns','data':json.dumps({'servers':[{'type':'bogus-type','tag':'x'}]})})
check('master->node: invalid DNS section refused by node check, nothing saved', not r['success'], r)
nc2,_=call(NODE,NT,'config'); check('node: dns unchanged after refused save', nc2['obj']['config'].get('dns')==nc['obj']['config'].get('dns'))
# dangerous op w/o confirm
r,_=call(MASTER,MT,'nodeRemote','POST',{'id':nid,'op':'panel.restart'})
check('master: panel.restart needs confirm', not r['success'] and 'confirm' in r['msg'], r)
r,_=call(MASTER,MT,'nodeRemote','POST',{'id':nid,'op':'shell'})
check('master: unknown op refused', not r['success'], r)
# delete inbound
r,_=call(MASTER,MT,'nodeRemote','POST',{'id':nid,'op':'save.inbounds','action':'del','data':json.dumps('e2e-vless')})
check('master->node: delete inbound', r['success'], r)
# idempotency on node: same request id twice
hdr={'X-Request-Id':'e2e-idem-0001'}
a,ha=call(NODE,NT,'save','POST',{'object':'inbounds','action':'new','data':json.dumps(dict(ib,tag='idem',listen_port=24444))},headers=hdr)
b,hb=call(NODE,NT,'save','POST',{'object':'inbounds','action':'new','data':json.dumps(dict(ib,tag='idem',listen_port=24444))},headers=hdr)
ni,_=call(NODE,NT,'inbounds'); cnt=[x['tag'] for x in ni['obj']['inbounds']].count('idem')
check('node: same X-Request-Id applied once (replayed)', a['success'] and hb.get('X-Idempotent-Replay')=='1' and cnt==1, (a.get('msg'),hb.get('X-Idempotent-Replay'),cnt))
call(NODE,NT,'save','POST',{'object':'inbounds','action':'del','data':json.dumps('idem')})
# ---- inbound form with a node target (create/edit/delete on the node, client attach) ----
import base64
def wait(fn, secs=20):
    end=time.time()+secs
    while time.time()<end:
        v=fn()
        if v: return v
        time.sleep(1)
    return fn()
def mclient(name):
    c,_=call(MASTER,MT,'clients'); return next((x for x in c['obj']['clients'] if x['name']==name), None)
def mclient_full(cid):
    c,_=call(MASTER,MT,'clients',q={'id':cid}); return c['obj']['clients'][0]
def ninbound(tag):
    ni,_=call(NODE,NT,'inbounds'); return next((x for x in (ni['obj']['inbounds'] or []) if x['tag']==tag), None)
def mreplica(tag):
    mi,_=call(MASTER,MT,'inbounds'); return next((x for x in (mi['obj']['inbounds'] or []) if x['tag']==tag), None)
def nclients():
    c,_=call(NODE,NT,'clients'); return {x['name']:x for x in (c['obj']['clients'] or [])}
def ccfg(name): return {'vless':{'name':name,'uuid':'8b5e6d3c-3f3e-4c1a-9b1e-%012x' % (abs(hash(name)) % (1<<48))}}
def client(name, inbounds):
    return {'enable':True,'name':name,'config':ccfg(name),'inbounds':inbounds,'links':[],'volume':0,'expiry':0,'desc':'','group':''}
# a local master inbound, so a client can carry master + node inbounds
r,_=call(MASTER,MT,'save','POST',{'object':'inbounds','action':'new','data':json.dumps({'type':'vless','tag':'m-vless','listen':'::','listen_port':23443,'tls':{}})})
check('master: local inbound for the mixed client', r['success'], r)
mloc=mreplica('m-vless')
r,_=call(MASTER,MT,'save','POST',{'object':'clients','action':'new','data':json.dumps(client('e2e-alice',[mloc['id']]))})
check('master: client alice (master inbound)', r['success'], r)
alice=mclient('e2e-alice')
f,_=call(MASTER,MT,'nodeInboundForm',q={'nodeId':nid})
check('target: form data for a node (its TLS list)', f['success'] and isinstance(f['obj'].get('tls'), list), f)
tib={'id':0,'type':'vless','tag':'e2e-tgt','listen':'::','listen_port':24460,'tls':{},'users':[]}
form={'action':'new','nodeId':nid,'data':json.dumps(tib),'initUsers':str(alice['id']),'requestId':'e2e-tgt-0001'}
r,_=call(MASTER,MT,'nodeInboundSave','POST',form)
check('target: create on node from the master form', r['success'] and r['obj'] and r['obj'].get('node_id')==nid, r)
rid=(r.get('obj') or {}).get('id')
check('target: inbound exists on the node', ninbound('e2e-tgt') is not None)
check('target: adopted at once as a replica (no import step)', (mreplica('e2e-tgt') or {}).get('node_id')==nid, mreplica('e2e-tgt'))
r2,_=call(MASTER,MT,'nodeInboundSave','POST',form)
ni,_=call(NODE,NT,'inbounds'); cnt=[x['tag'] for x in ni['obj']['inbounds']].count('e2e-tgt')
check('target: retried submit is a replay (same replica, one inbound on node)', r2['success'] and (r2.get('obj') or {}).get('id')==rid and cnt==1, (r2.get('msg'),cnt))
r,_=call(MASTER,MT,'nodeInboundSave','POST',dict(form, data=json.dumps(dict(tib,tag='e2e-tgt2')), requestId='e2e-tgt-0002', initUsers=''))
check('target: port taken on the node refused before writing', not r['success'] and 'already used' in r['msg'] and ninbound('e2e-tgt2') is None, r)
nc=wait(lambda: (lambda c: c if 'e2e-alice' in c else None)(nclients()))
na=(nc or {}).get('e2e-alice') or {}
check('target: client pushed to the node in @cluster', na.get('group')=='@cluster', na)
check('target: node client bound to the node inbound', ninbound('e2e-tgt') and ninbound('e2e-tgt')['id'] in (na.get('inbounds') or []), (na.get('inbounds'), ninbound('e2e-tgt')))
def node_link(): 
    links=json.dumps(mclient_full(alice['id']).get('links'))
    return links if '[local-node]' in links else None
check('target: node link in the client (with [node] prefix)', wait(node_link), mclient_full(alice['id']).get('links'))
def sub_text():
    try:
        with urllib.request.urlopen('http://127.0.0.1:23096/sub/e2e-alice', timeout=10) as resp: body=resp.read()
        try: return base64.b64decode(body+b'===').decode('utf-8','replace')
        except Exception: return body.decode('utf-8','replace')
    except Exception as e: return 'ERR '+str(e)
st=sub_text()
check('target: one subscription with master and node servers', ':23443' in st and ':24460' in st, st[:400])
# client attach from the client form: master + node inbound in one save
# sync=true: the immediate node fanout the web UI's client form always gets
r,_=call(MASTER,MT,'save','POST',{'object':'clients','action':'new','sync':'true','data':json.dumps(client('e2e-bob',[mloc['id'],rid]))})
check('target: client form with master + node inbounds saved', r['success'], r)
nb=wait(lambda: nclients().get('e2e-bob'))
check('target: that client reaches the node at once (@cluster)', nb and nb.get('group')=='@cluster' and ninbound('e2e-tgt')['id'] in (nb.get('inbounds') or []), (nb, mclient('e2e-bob'), call(MASTER,MT,'nodeDetail',q={'id':nid})[0]['obj'].get('status')))
# edit the replica from the master form: applied on the node
f,_=call(MASTER,MT,'nodeInboundForm',q={'id':rid})
check('target: edit form loads the node inbound', f['success'] and f['obj']['inbound']['tag']=='e2e-tgt' and f['obj']['inbound']['id']==rid, f.get('msg'))
eib=dict(f['obj']['inbound'], listen_port=24461)
r,_=call(MASTER,MT,'nodeInboundSave','POST',{'action':'edit','data':json.dumps(eib),'requestId':'e2e-tgt-0003'})
check('target: edit applied on the node', r['success'] and (ninbound('e2e-tgt') or {}).get('listen_port')==24461, (r.get('msg'), ninbound('e2e-tgt')))
check('target: replica refreshed after edit', (mreplica('e2e-tgt') or {}).get('listen_port')==24461, mreplica('e2e-tgt'))
check('target: node client keeps its binding after edit', 'e2e-alice' in nclients() and ninbound('e2e-tgt')['id'] in (nclients()['e2e-alice'].get('inbounds') or []))
r,_=call(MASTER,MT,'nodeInboundSave','POST',{'action':'edit','data':json.dumps(dict(eib,tag='e2e-renamed')),'requestId':'e2e-tgt-0004'})
check('target: rename of a node inbound refused', not r['success'], r)
# delete on the node
r,_=call(MASTER,MT,'nodeInboundSave','POST',{'action':'del','data':json.dumps('e2e-tgt'),'requestId':'e2e-tgt-0005'})
check('target: delete on node', r['success'] and ninbound('e2e-tgt') is None and mreplica('e2e-tgt') is None, (r.get('msg'), ninbound('e2e-tgt'), mreplica('e2e-tgt')))
check('target: client unbound from the deleted replica', rid not in (mclient('e2e-alice') or {}).get('inbounds', []), mclient('e2e-alice'))
for nm in ('e2e-alice','e2e-bob'):
    c=mclient(nm)
    if c:
        r,_=call(MASTER,MT,'save','POST',{'object':'clients','action':'del','sync':'true','data':json.dumps(c['id'])})
        check('target: cleanup client '+nm, r['success'], r)
call(MASTER,MT,'save','POST',{'object':'inbounds','action':'del','data':json.dumps('m-vless')})
# the section above spends most of the per-node remote-op burst (20, refilled
# at 2/s); let it refill so the checks below test their own guards
time.sleep(10)
# ---- hardening / extras ----
import sqlite3
raw=sqlite3.connect('master/3s-ui.db').execute('select token from nodes where id=?',(nid,)).fetchone()[0]
check('master: node token sealed at rest (enc:v1:)', raw.startswith('enc:v1:') and NT not in raw, raw[:12])
st,_=call(NODE,NT,'status',q={'r':'sys'}); rev1=(st['obj'].get('sys') or {}).get('inboundsRev')
check('node: status carries sys.inboundsRev', bool(rev1), st['obj'].get('sys'))
call(NODE,NT,'save','POST',{'object':'inbounds','action':'new','data':json.dumps(dict(ib,tag='direct-edit',listen_port=24445))})
st,_=call(NODE,NT,'status',q={'r':'sys'}); rev2=(st['obj'].get('sys') or {}).get('inboundsRev')
check('node: inboundsRev changes on a direct edit', rev2 and rev2!=rev1, (rev1,rev2))
call(NODE,NT,'save','POST',{'object':'inbounds','action':'del','data':json.dumps('direct-edit')})
for op,params in (('nodes.tree',None),('onlines',None),('panel.updateInfo',None),('panel.updateStatus',None),('stats',{'resource':'inbound','tag':'e2e-vless','period':'day'})):
    f={'id':nid,'op':op}
    if params: f['params']=json.dumps(params)
    r,_=call(MASTER,MT,'nodeRemote','POST',f)
    check('master->node: '+op, r['success'], r.get('msg'))
r,_=call(MASTER,MT,'nodeRemote','POST',{'id':nid,'op':'panel.update'})
check('master: panel.update needs confirm', not r['success'] and 'confirm' in r['msg'], r)
q,_=call(MASTER,MT,'nodeQueue',q={'id':nid}); check('master: nodeQueue list', q['success'], q)
# token rotation
r,_=call(MASTER,MT,'nodeTokenRotate','POST',{'id':nid})
check('master: token rotation', r['success'], r)
old_ok,_=call(NODE,NT,'nodeHello')
check('node: old token revoked after rotation', not old_ok['success'], old_ok)
r,_=call(MASTER,MT,'nodeRemote','POST',{'id':nid,'op':'hello'})
check('master->node: works with rotated token', r['success'], r)
# bulk + detail + ops
r,_=call(MASTER,MT,'nodeBulk','POST',{'ids':json.dumps([nid]),'op':'subExclude'})
check('master: bulk subExclude', r['success'] and r['obj'][0]['ok'], r)
d,_=call(MASTER,MT,'nodeDetail',q={'id':nid})
check('master: nodeDetail', d['success'] and d['obj']['node']['subExclude'] is True, d.get('msg'))
o,_=call(MASTER,MT,'nodeOps',q={'id':nid})
ops=[x['op'] for x in o['obj']['rows']]
check('master: audit trail has writes', 'save.inbounds' in ops and 'token.rotate' in ops and 'config.route' in ops, ops)
check('master: audit has no tokens', NT not in json.dumps(o))
time.sleep(6)
d,_=call(MASTER,MT,'nodeDetail',q={'id':nid})
print('   health:', d['obj']['status'].get('health'), d['obj']['status'].get('reasons'), 'api', d['obj']['status'].get('apiVersion'))
check('master: heartbeat health derived', d['obj']['status'].get('health') in ('online','degraded','syncing'), d['obj']['status'])
print('\nSUMMARY: %d/%d passed' % (sum(1 for _,ok in res if ok), len(res)))
