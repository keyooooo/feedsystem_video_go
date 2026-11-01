"""Dedicated-account server smoke test. Secrets remain in an ignored local file."""
import json, os, secrets, time, urllib.request
from pathlib import Path
BASE = os.environ.get('API_PROXY_TARGET', 'http://8.134.84.151:8080').rstrip('/')
store = Path(__file__).resolve().parent.parent / '.e2e-accounts.local'
def post(path, body, token=None):
    headers = {'Content-Type': 'application/json'}
    if token: headers['Authorization'] = 'Bearer ' + token
    req = urllib.request.Request(BASE + path, json.dumps(body).encode(), headers)
    with urllib.request.urlopen(req, timeout=30) as response:
        return json.load(response)
if store.exists():
    accounts = json.loads(store.read_text())
else:
    accounts = []
    store.touch(mode=0o600)
    for index in range(2):
        account = {'username': f'refactor_e2e_{int(time.time())}_{index}', 'password': secrets.token_urlsafe(24)}
        post('/account/register', account)
        accounts.append(account)
        store.write_text(json.dumps(accounts))
    store.chmod(0o600)
assert len(accounts) == 2, 'Dedicated-account setup incomplete; inspect local credential file.'
a, b = [post('/account/login', account) for account in accounts]
assert a['token'] and a['refresh_token']
assert post('/account/refresh', {'refresh_token': a['refresh_token']})['token']
assert post('/account/getProfile', {'account_id': a['account_id']})['account']['id'] == a['account_id']
for path, body in [('/feed/listLatest', {'limit': 2, 'latest_time': 0}), ('/feed/listLikesCount', {'limit': 2}), ('/feed/listByPopularity', {'limit': 2, 'as_of': 0, 'offset': 0})]:
    result = post(path, body, a['token'])
    assert 'has_more' in result and 'video_list' in result
post('/social/follow', {'vlogger_id': b['account_id']}, a['token'])
post('/social/unfollow', {'vlogger_id': b['account_id']}, a['token'])
message = 'React migration verification ' + str(int(time.time()))
post('/message/send', {'to_id': b['account_id'], 'content': message}, a['token'])
conversation = post('/message/list', {'peer_id': a['account_id']}, b['token'])
assert any(m['content'] == message for m in conversation['messages'])
print('PASS: dedicated accounts, login, refresh, profile, three feed contracts, follow/unfollow and direct messages.')

# Exercise uploads using synthetic media owned by these dedicated accounts only.
import hashlib, struct, uuid
fixture = Path(__file__).resolve().parent.parent / 'e2e' / 'fixtures'
clip = (fixture / 'clip.mp4').read_bytes()
padding = max(8, 6 * 1024 * 1024 - len(clip))
clip += struct.pack('>I4s', padding, b'free') + bytes(padding - 8)
chunk_size = 5 * 1024 * 1024
init = post('/video/chunk/init', {'filename':'refactor-verification.mp4','file_size':len(clip),'chunk_size':chunk_size,'total_chunks':2,'file_hash':hashlib.md5(clip).hexdigest()}, a['token'])
def multipart(path, fields, name, data, content_type):
    boundary = uuid.uuid4().hex
    parts = []
    for key, value in fields.items():
        parts.append(f'--{boundary}\r\nContent-Disposition: form-data; name="{key}"\r\n\r\n{value}\r\n'.encode())
    parts.extend([f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{name}"\r\nContent-Type: {content_type}\r\n\r\n'.encode(),data,f'\r\n--{boundary}--\r\n'.encode()])
    req=urllib.request.Request(BASE+path,b''.join(parts),{'Content-Type':'multipart/form-data; boundary='+boundary,'Authorization':'Bearer '+a['token']})
    with urllib.request.urlopen(req,timeout=90) as response: return json.load(response)
for index in range(2):
    if index in init['uploaded_chunks']: continue
    part=clip[index*chunk_size:(index+1)*chunk_size]
    multipart('/video/chunk/upload',{'upload_id':init['upload_id'],'chunk_index':index,'chunk_hash':hashlib.md5(part).hexdigest()},'chunk.bin',part,'application/octet-stream')
status=post('/video/chunk/status',{'upload_id':init['upload_id']},a['token'])
assert len(status['uploaded_chunks'])==2
merged=post('/video/chunk/complete',{'upload_id':init['upload_id']},a['token'])
cover=multipart('/video/uploadCover',{},'verification.png',(fixture/'poster.png').read_bytes(),'image/png')
play_url=merged.get('url') or merged['play_url']
published=post('/video/publish',{'title':'React verification '+str(int(time.time())),'description':'Synthetic isolated migration test fixture','play_url':play_url,'cover_url':cover.get('url') or cover['cover_url']},a['token'])
video_id=published['id']
req=urllib.request.Request(play_url,headers={'Range':'bytes=0-63'})
with urllib.request.urlopen(req,timeout=30) as response:
    assert response.status==206
    assert len(response.read())==64
post('/like/like',{'video_id':video_id},b['token'])
post('/like/unlike',{'video_id':video_id},b['token'])
marker='Migration comment '+str(int(time.time()))
post('/comment/publish',{'video_id':video_id,'content':marker},b['token'])
found=None
for _ in range(12):
    values=post('/comment/listAll',{'video_id':video_id},b['token']) or []
    found=next((c for c in values if c['content']==marker),None)
    if found: break
    time.sleep(1)
assert found, 'Comment did not appear within eventual-consistency window'
post('/comment/delete',{'comment_id':found['id']},b['token'])
(fixture.parent.parent/'.live-report.local').write_text(json.dumps({'video_id':video_id,'play_url':play_url,'cover_url':cover.get('url') or cover['cover_url']}))
print('PASS: 6MB two-chunk upload, status, merge, cover, publish, media Range, likes and eventual comment write/delete.')
