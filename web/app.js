'use strict';
let t = {peers: [], vertices: [], edges: [], routes: {}, index: 0};
const dark = true;
const $ = id => document.getElementById(id), byID = new Map(t.vertices.map(v => [v.id,v]));
const peer = i => t.peers.find(p => p.index === Number(i));
const isHost = v => v?.proto === 'udp' && v.port === 0;
const label = v => v ? `${v.port && v.addr.includes(':') ? '['+v.addr+']' : v.addr}${v.port ? ':'+v.port : ''}` : '—';
const epFor = index => t.vertices.filter(v => v.owner === index && v.endpoint);
const ipVertex = index => t.vertices.find(v => isHost(v) && v.addr === peer(index)?.intip);
const active = p => !!ipVertex(p.index);
const colors = dark ? ['#93c4a4','#9aaed0','#c0b48c','#7cb9bf'] : ['#6484eb','#8e7ed0','#55a5a1','#c89a65'];
const nodeColor = index => colors[Math.max(0,t.peers.findIndex(p=>p.index===index))%colors.length];
let mode = 'endpoints', selected = 0, topologyKey = '', ready = false;
const cy = cytoscape({container:$('graph'), minZoom:.1,maxZoom:3,wheelSensitivity:.2, style:[
 {selector:'node',style:{'label':'data(label)','text-wrap':'wrap','font-family':'system-ui','font-size':9,'color':dark?'#9cacbb':'#7d8ba2','text-valign':'bottom','text-margin-y':6,'background-color':'data(color)','border-width':1,'border-color':dark?'#456150':'#d7dfef',width:12,height:12}},
 {selector:'node.host',style:{shape:'round-rectangle',width:105,height:52,'background-color':dark?'#21352e':'#ffffff','border-color':'data(color)','border-width':1,'text-valign':'center','text-margin-y':0,'font-size':11,'font-weight':550,'color':dark?'#c6dacd':'#415576','text-max-width':120}},
 {selector:'node.ip',style:{width:30,height:30,'border-width':5,'border-color':dark?'#24362e':'#edf1ff','font-size':10,'font-weight':550,'color':dark?'#c6dacd':'#4f607d'}},
 {selector:'node.offline',style:{'background-color':dark?'#18252c':'#f7f9fc','border-color':dark?'#33404d':'#d6ddea','border-style':'dashed','color':dark?'#596c7c':'#a4afbe'}},
 {selector:'edge',style:{'curve-style':'bezier',width:1,'target-arrow-shape':'triangle','arrow-scale':.6,'line-color':dark?'#657e6e':'#bac6dc','target-arrow-color':dark?'#7b9a83':'#acbad0',opacity:.28}},
 {selector:'edge.aggregate',style:{width:1.5,opacity:.65,'curve-style':'bezier','control-point-step-size':35,'label':'data(count)','font-size':9,'color':dark?'#89a794':'#8a9dbd','text-background-color':dark?'#111b25':'#fff','text-background-opacity':1,'text-background-padding':3,'text-rotation':'autorotate'}},
 {selector:'edge.attachment',style:{'line-style':'dotted',opacity:.35}},
 {selector:'.dim',style:{opacity:.09}},
 {selector:'edge.focus',style:{opacity:1,width:2.3,'line-color':dark?'#b8d4a1':'#6684e9','target-arrow-color':dark?'#b8d4a1':'#6684e9','z-index':10}},
 {selector:'node.focus',style:{'border-color':dark?'#d0e7a9':'#6788ff','border-width':3}},
 {selector:':selected',style:{'border-color':dark?'#d0e7a9':'#6788ff','border-width':3}}
]});
function clear() { cy.elements().removeClass('dim focus'); }
function layout() {
 const owners = [...new Set([...t.peers.map(p => p.index), ...t.vertices.map(v => v.owner)])];
 const positions = Object.fromEntries(owners.map((owner, i) => {
  const angle = i * 2 * Math.PI / owners.length - Math.PI / 2;
  const radius = Math.max(230, owners.length * 55);
  return [owner, [Math.cos(angle) * radius, Math.sin(angle) * radius]];
 }));
 if(mode === 'hosts') cy.nodes().positions(n => {const p=positions[n.data('owner')] || [0,0];return {x:p[0],y:p[1]};});
 else for(const [owner,center] of Object.entries(positions)) {
  const group=cy.nodes().filter(n=>n.data('owner')===Number(owner));
  const endpoints=group.filter(n=>!n.hasClass('ip') && !n.hasClass('offline'));
  group.filter(n=>n.hasClass('ip') || n.hasClass('offline')).positions(()=>({x:center[0],y:center[1]}));
  endpoints.positions((n,i)=>({x:center[0]+Math.cos(i*2*Math.PI/endpoints.length)*100,y:center[1]+Math.sin(i*2*Math.PI/endpoints.length)*72}));
 }
 cy.fit(undefined,40);
}
function buildGraph(preserve = false) {
 const positions = new Map(cy.nodes().map(n => [n.id(), {...n.position()}]));
 const elements=[];
 if(mode === 'hosts') {
  for(const p of t.peers) elements.push({data:{id:'n'+p.index,owner:p.index,label:`${p.name}\n${p.intip}`,color:nodeColor(p.index)},classes:'host'+(active(p)?'':' offline')});
  const pairs=new Map();
  for(const e of t.edges) {const a=byID.get(e.source)?.owner,b=byID.get(e.target)?.owner;if(a && b && a!==b){const id=`n${a}:n${b}`;const old=pairs.get(id);if(old) old.data.count++;else pairs.set(id,{data:{id,source:'n'+a,target:'n'+b,count:1},classes:'aggregate'});}}
  elements.push(...pairs.values());
 } else {
  // A vertex attached only to its own host, with no link to or from another node, is left out.
  const linked = new Set();
  for(const e of t.edges) if(!isHost(byID.get(e.source)) && !isHost(byID.get(e.target))) {linked.add(e.source);linked.add(e.target);}
  const shown = new Set(t.vertices.filter(v => isHost(v) || linked.has(v.id)).map(v => v.id));
  for(const v of t.vertices) if(shown.has(v.id)) elements.push({data:{id:v.id,owner:v.owner,vertex:v,label:!isHost(v)?label(v):`${peer(v.owner)?.name || 'unregistered'}\n${v.addr}`,color:nodeColor(v.owner)},classes:isHost(v)?'ip':''});
  for(const p of t.peers) if(!active(p)) elements.push({data:{id:'n'+p.index,owner:p.index,label:`${p.name}\n${p.intip}`,color:nodeColor(p.index)},classes:'host offline'});
  for(const e of t.edges) if(shown.has(e.source) && shown.has(e.target)) elements.push({data:{id:e.source+':'+e.target,source:e.source,target:e.target},classes:isHost(byID.get(e.source)) || isHost(byID.get(e.target))?'attachment':''});
 }
 cy.batch(()=>{cy.elements().remove();cy.add(elements);});
 $('hosts-mode').classList.toggle('active',mode==='hosts');$('endpoints-mode').classList.toggle('active',mode==='endpoints');
 if (!preserve || cy.nodes().some(n => !positions.has(n.id()))) layout();
 else cy.nodes().positions(n => positions.get(n.id()));
}
function inspect(index, highlight=true) {
 selected=index;const p=peer(index); if(!p)return;
 $('selected-name').textContent=p.name;$('selected-ip').textContent=p.intip;
 $('selected-note').textContent=index===t.index?'Локальная нода.':active(p)?'Есть в графе достижимости.':'В registry. В снимке маршрута до ноды нет.';
 $('endpoints').replaceChildren();const endpoints=epFor(index);$('endpoint-count').textContent=endpoints.length;
 for(const e of endpoints){const row=document.createElement('div');row.className='endpoint-row';const proto=document.createElement('span');proto.textContent=e.proto.toUpperCase();const value=document.createTextNode(label(e));const button=document.createElement('button');button.textContent='↗';button.title='Показать endpoint';button.onclick=()=>{setPage('endpoints');focusVertex(e.id);};row.append(proto,value,button);$('endpoints').append(row);}
 if(!endpoints.length){const note=document.createElement('p');note.textContent='Нет объявленных точек входа.';note.style.fontSize='10px';$('endpoints').append(note);}
 if(highlight){clear();const nodes=cy.nodes().filter(n=>n.data('owner')===index);const edges=nodes.connectedEdges();cy.elements().addClass('dim');nodes.union(edges).union(edges.connectedNodes()).removeClass('dim');nodes.addClass('focus');edges.addClass('focus');}
}
function focusVertex(id){clear();const n=cy.getElementById(id);cy.elements().addClass('dim');n.closedNeighborhood().removeClass('dim');n.connectedEdges().addClass('focus');n.addClass('focus');}
function showPath(path,description){
 clear();if(!path){$('route-summary').textContent='В этом снимке пути нет.';return;}
 const ids=new Set(path.map(e=>mode==='hosts'?`n${byID.get(e.source)?.owner}:n${byID.get(e.target)?.owner}`:`${e.source}:${e.target}`));
 const edges=cy.edges().filter(e=>ids.has(e.id()));cy.elements().addClass('dim');edges.union(edges.connectedNodes()).removeClass('dim').addClass('focus');
 $('route-summary').textContent=description+'\n'+path.filter(e=>byID.get(e.source)?.owner!==byID.get(e.target)?.owner).map(e=>`${label(byID.get(e.source))} → ${label(byID.get(e.target))}`).join('\n');
}
function localRoute(index){const p=peer(index),path=t.routes[p.intip+':0'];inspect(index,false);$('route-dest').value=String(index);showPath(path,`${peer(t.index).name} → ${p.name} · ${path?.filter(e=>byID.get(e.source)?.owner!==byID.get(e.target)?.owner).length || 0} переходов`);}
function renderPeers() {
 const destination = $('route-dest').value;
 $('route-dest').replaceChildren(new Option('Выбрать назначение',''));
 $('config-cards').replaceChildren();
for(const p of t.peers){
 if(p.index!==t.index)$('route-dest').add(new Option(`${peer(t.index).name} → ${p.name} · ${p.intip}`,p.index));
 if(!p.endpoint.length){const card=document.createElement('article');card.className='config-card';const kicker=document.createElement('div');kicker.className='kicker';kicker.textContent='EPHEMERAL NODE / '+p.index;const name=document.createElement('h2');name.textContent=p.name;const ip=document.createElement('div');ip.className='mono';ip.textContent=p.intip;const pub=document.createElement('div');pub.className='pub';pub.textContent='PUBLIC KEY\n'+p.pub;const note=document.createElement('p');note.textContent='Полный конфиг · без приватного ключа';const link=document.createElement('a');link.href=`api/config?node=${p.index}`;link.download=`mesh-${p.index}.json`;link.textContent=`Скачать mesh-${p.index}.json ↓`;card.append(kicker,name,ip,pub,note,link);$('config-cards').append(card);}
}
 $('route-dest').value = destination;
}
$('fit').onclick=()=>cy.fit(undefined,40);$('reset').onclick=layout;
$('route-dest').onchange=()=>{if($('route-dest').value)localRoute(Number($('route-dest').value));else clear();};
cy.on('tap','node',event=>{const n=event.target;if(peer(n.data('owner')))inspect(n.data('owner'));if(mode==='endpoints')focusVertex(n.id());});
cy.on('tap','edge',event=>{clear();const e=event.target;e.addClass('focus');$('selected-name').textContent='Направленная связь';$('selected-ip').textContent='';$('selected-note').textContent=mode==='hosts'?`${peer(e.source().data('owner')).name} → ${peer(e.target().data('owner')).name}: ${e.data('count')} рёбер между endpoint`:`${label(e.source().data('vertex'))} → ${label(e.target().data('vertex'))}`;});
cy.on('tap',event=>{if(event.target===cy)clear();});
function setPage(page) {
 const graphPage = page === 'hosts' || page === 'endpoints';
 $('topology-page').hidden = !graphPage;
 $('matrix-page').hidden = page !== 'matrix';
 $('configs-page').hidden = page !== 'configs';
 for (const button of document.querySelectorAll('[data-page]')) {
  const active = button.dataset.page === page;
  button.classList.toggle('active', active);
  button.setAttribute('aria-selected', String(active));
 }
 if (graphPage) {
  const changed = mode !== page || !ready || cy.nodes().length === 0;
  mode = page;
  cy.resize();
  if (changed) { buildGraph(); ready = t.peers.length > 0; }
 }
}
function graphPath(source, target) {
 const start = ipVertex(source)?.id, finish = ipVertex(target)?.id;
 if (!start || !finish) return null;
 const distance = new Map([[start, 0]]), previous = new Map(), pending = new Set(byID.keys());
 while (pending.size) {
  let current, best = Infinity;
  for (const id of pending) {
   const cost = distance.get(id) ?? Infinity;
   if (cost < best) { current = id; best = cost; }
  }
  if (current === undefined || current === finish) break;
  pending.delete(current);
  for (const edge of t.edges) {
   if (edge.source !== current || !pending.has(edge.target)) continue;
   const cost = best + (byID.get(edge.source).owner !== byID.get(edge.target).owner ? 1 : 0);
   if (cost < (distance.get(edge.target) ?? Infinity)) {
    distance.set(edge.target, cost);
    previous.set(edge.target, edge.source);
   }
  }
 }
 if (!distance.has(finish)) return null;
 const path = [];
 for (let to = finish; to !== start;) {
  const from = previous.get(to);
  path.unshift({source: from, target: to});
  to = from;
 }
 return {path, hops: distance.get(finish)};
}
function renderMatrix() {
 const table = document.createElement('table');
 const caption = document.createElement('caption');
 caption.className = 'sr-only';
 caption.textContent = 'Минимальное число хопов по направленному графу. Строка — источник, столбец — назначение. Прочерк — пути нет.';
 table.append(caption);
 const head = document.createElement('thead'), row = document.createElement('tr');
 const corner = document.createElement('th');
 corner.textContent = 'От ↓ / До →';
 row.append(corner);
 for (const p of t.peers) {
  const cell = document.createElement('th');
  cell.scope = 'col';
  cell.textContent = p.name;
  const ip = document.createElement('small');
  ip.textContent = p.intip;
  cell.append(ip);
  row.append(cell);
 }
 head.append(row);
 table.append(head);
 const body = document.createElement('tbody');
 for (const from of t.peers) {
  const row = document.createElement('tr'), name = document.createElement('th');
  name.scope = 'row';
  name.textContent = from.name;
  const ip = document.createElement('small');
  ip.textContent = from.intip;
  name.append(ip);
  row.append(name);
  for (const to of t.peers) {
   const cell = document.createElement('td');
   const route = from.index === to.index ? null : graphPath(from.index, to.index);
   if (from.index === to.index) {
    cell.textContent = '0';
    cell.className = 'diagonal';
   } else if (!route) {
    cell.textContent = '—';
    cell.className = 'no-path';
    cell.title = `${from.name} → ${to.name}: пути нет`;
   } else {
    const button = document.createElement('button');
    button.textContent = route.hops;
    button.title = `${from.name} → ${to.name}: хопов ${route.hops}. Показать путь`;
    button.onclick = () => {
     setPage('endpoints');
     inspect(to.index, false);
     $('route-dest').value = '';
     showPath(route.path, `${from.name} → ${to.name} · хопов ${route.hops}`);
    };
    cell.append(button);
   }
   row.append(cell);
  }
  body.append(row);
 }
 table.append(body);
 $('matrix').replaceChildren(table);
}
for (const button of document.querySelectorAll('[data-page]')) button.onclick = () => setPage(button.dataset.page);
setPage(location.pathname === '/config' ? 'configs' : 'endpoints');
async function refresh() {
 try {
  const response = await fetch('api/topology', {cache: 'no-store', signal: AbortSignal.timeout(10000)});
  if (!response.ok) throw new Error(`Контролька: HTTP ${response.status}`);
  const next = await response.json();
  next.peers = next.peers.map(p => ({...p, name: p.name || `node ${p.index}`}));
  const key = JSON.stringify([next.index, next.peers, next.vertices, next.edges, next.routes]);
  t = next;
  if (key !== topologyKey) {
   topologyKey = key;
   byID.clear();
   for (const v of t.vertices) byID.set(v.id, v);
   renderPeers();
   renderMatrix();
   const graphHidden = $('topology-page').hidden;
   if (!graphHidden) buildGraph(ready);
   else ready = false;
   if (!peer(selected)) selected = t.index;
   inspect(selected, false);
   if ($('route-dest').value) localRoute(Number($('route-dest').value));
   ready = !graphHidden;
  }
  $('connection').classList.remove('error');
  $('connection').textContent = peer(t.index).name;
  $('connection').title = 'Обновлено: ' + new Date(t.time).toLocaleString();
 } catch (error) {
  $('connection').classList.add('error');
  $('connection').textContent = 'Нет связи с контролькой';
  $('connection').title = error.message;
 }
 setTimeout(refresh, 3000);
}
refresh();

window.addEventListener("resize", () => cy.resize());
