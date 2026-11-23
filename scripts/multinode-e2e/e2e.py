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
