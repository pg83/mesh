"""Chromium exercises the live web UI in the observer's network namespace."""
import json
import os
import time
from pathlib import Path
from playwright.sync_api import sync_playwright

SOURCE = Path(__file__).resolve().parent.parent / 'web' / 'app.js'
START = time.monotonic()


def phase(name):
    """Every step prints where it got to, so a slow run names its own step."""
    print(f'browser {time.monotonic() - START:6.1f}s {name}', flush=True)


def script_counts(functions, size):
    """The count V8 reports for every character: ranges arrive outermost first,
    so a nested one overwrites the enclosing count exactly where it applies."""
    counts = [None] * size
    for function in functions:
        for span in function['ranges']:
            for offset in range(span['startOffset'], min(span['endOffset'], size)):
                counts[offset] = span['count']
    return counts


def lcov(source, scripts, name):
    """V8 counts as an lcov record. A page load parses the script afresh, so the
    loads are merged by the highest count each character reached, and a line
    counts as executed when any of its own characters did."""
    counts = [None] * len(source)
    for functions in scripts:
        for offset, count in enumerate(script_counts(functions, len(source))):
            if count is not None and (counts[offset] is None or count > counts[offset]):
                counts[offset] = count
    lines, offset = [], 0
    for number, text in enumerate(source.split('\n'), start=1):
        measured = [counts[i] for i, character in enumerate(text, start=offset)
                    if not character.isspace() and counts[i] is not None]
        if measured:
            lines.append((number, max(measured)))
        offset += len(text) + 1
    record = ['TN:', f'SF:{name}']
    record += [f'DA:{number},{count}' for number, count in lines]
    record += [f'LF:{len(lines)}', f'LH:{sum(1 for _, count in lines if count)}', 'end_of_record']
    return '\n'.join(record) + '\n'


def coverage(session, path):
    source = SOURCE.read_text()
    scripts = [s['functions'] for s in session.send('Profiler.takePreciseCoverage')['result']
               if s['url'].endswith('/app.js')]
    assert scripts, 'the browser reported no coverage for app.js'
    Path(path).write_text(lcov(source, scripts, 'web/app.js'))


with sync_playwright() as p:
    browser = p.chromium.launch(args=['--no-sandbox'])
    page = browser.new_page(viewport={'width': 1440, 'height': 900}, accept_downloads=True)
    errors = []
    page.on('pageerror', lambda error: errors.append(str(error)))
    session = page.context.new_cdp_session(page)
    session.send('Profiler.enable')
    session.send('Profiler.startPreciseCoverage', {'callCount': False, 'detailed': True})
    phase('coverage armed')
    page.goto('http://127.0.0.1:8059/')
    page.wait_for_function('ready && t.peers.length === 3')
    phase('first snapshot')
    assert page.get_by_role('tab').count() == 4
    assert page.evaluate('cy.nodes().length') >= 6
    # Every endpoint circle shown is linked to a vertex of another node; lone attachments are hidden.
    assert page.evaluate('t.vertices.some(v => !(v.proto === "udp" && v.port === 0) && !cy.getElementById(v.id).length)')
    assert page.evaluate('cy.nodes().filter(n => !n.hasClass("ip") && !n.hasClass("host")).every(n => n.connectedEdges().connectedNodes().some(m => m.id() !== n.id() && !m.hasClass("ip")))')
    assert page.evaluate('getComputedStyle(document.querySelector(".graph-panel")).borderWidth') == '0px'
    assert page.evaluate('getComputedStyle(document.querySelector(".inspector")).borderWidth') == '0px'
    assert page.evaluate('getComputedStyle(document.querySelector("main")).padding') == '0px'
    # Dragging/zooming survives periodic refreshes.
    page.evaluate('cy.zoom(1.3); cy.pan({x: 71, y: 83}); cy.nodes()[0].position({x:321,y:123})')
    page.wait_for_timeout(3500)
    assert page.evaluate('cy.zoom()') == 1.3
    assert page.evaluate('cy.pan()') == {'x': 71, 'y': 83}
    assert page.evaluate('cy.nodes()[0].position()') == {'x': 321, 'y': 123}
    phase('drag survived a refresh')
    # The inspector and the destination list are rebuilt whenever the topology
    # changes, which a live mesh does every few seconds. Serve the snapshot the
    # page already holds so the elements below stay put while they are used;
    # the page keeps polling, it just keeps reading the same answer.
    frozen = page.evaluate('JSON.stringify(t)')
    page.route('**/api/topology', lambda route: route.fulfill(
        status=200, content_type='application/json', body=frozen))
    # The panel buttons restore the view the dragging moved.
    page.get_by_role('button', name='Fit ↗', exact=True).click()
    assert page.evaluate('cy.zoom()') > 0
    page.get_by_role('button', name='Layout', exact=True).click()
    # Tapping a vertex selects its owner and highlights the vertex itself.
    page.evaluate('cy.nodes().filter(n => !n.hasClass("ip"))[0].emit("tap")')
    assert page.evaluate('cy.elements(".focus").length') >= 1
    assert page.evaluate('$("selected-name").textContent')
    # An advertised endpoint of the selected node leads back to its circle.
    page.evaluate('cy.nodes().filter(n => n.hasClass("ip") && n.data("owner") === 1)[0].emit("tap")')
    assert page.evaluate('$("endpoint-count").textContent') != '0'
    page.locator('#endpoints button').first.click()
    assert page.evaluate('cy.elements(".focus").length') >= 1
    # Tapping a link describes it, tapping the background clears the highlight.
    page.evaluate('cy.edges()[0].emit("tap")')
    assert page.evaluate('$("selected-name").textContent') == 'Directed link'
    page.evaluate('cy.emit("tap")')
    assert page.evaluate('cy.elements(".focus").length') == 0
    # The route selector highlights the local route and then drops it.
    page.select_option('#route-dest', label=[o for o in page.locator('#route-dest option').all_text_contents() if '→' in o][0])
    assert 'hops' in page.evaluate('$("route-summary").textContent')
    assert page.evaluate('cy.edges(".focus").length') >= 1
    page.select_option('#route-dest', value='')
    assert page.evaluate('cy.elements(".focus").length') == 0
    phase('graph interactions')
    page.get_by_role('tab', name='Hosts', exact=True).click()
    assert page.evaluate('cy.nodes().length') == 3
    # In this mode a link carries how many endpoint pairs it stands for.
    page.evaluate('cy.edges()[0].emit("tap")')
    assert 'edges between endpoints' in page.evaluate('$("selected-note").textContent')
    page.get_by_role('tab', name='Matrix', exact=True).click()
    page.wait_for_selector('#matrix-page:not([hidden])')
    hop = page.get_by_role('button', name='2', exact=True).first
    assert 'a → work' in (hop.get_attribute('title') or '')
    hop.click()
    assert page.get_by_role('tab', name='Endpoint', exact=True).get_attribute('aria-selected') == 'true'
    assert page.evaluate('cy.edges(".focus").length') >= 2
    phase('matrix')
    page.get_by_role('tab', name='Configs', exact=True).click()
    with page.expect_download() as download:
        page.get_by_role('link', name='Download mesh-2.json ↓').click()
    config = json.loads(Path(download.value.path()).read_text())
    assert config['index'] == 2 and 'key' not in config
    assert [peer['index'] for peer in config['registry']] == [1, 2]
    assert config.get('registry_version', 1) == 1
    page.goto('http://127.0.0.1:8059/config')
    page.wait_for_selector('#config-cards .config-card')
    assert page.get_by_role('tab', name='Configs', exact=True).get_attribute('aria-selected') == 'true'
    page.get_by_role('tab', name='Endpoint', exact=True).click()
    page.wait_for_function('cy.nodes().length > 3')
    if artifacts := os.environ.get('MESH_TEST_ARTIFACTS'):
        Path(artifacts).mkdir(parents=True, exist_ok=True)
        page.screenshot(path=str(Path(artifacts) / 'mesh-web.png'))
    phase('configs')
    # An update that keeps the vertices the graph already shows must land on the
    # positions they already have, and a peer nothing reaches must read as such
    # in the matrix. Both come from one answer: the same topology with every
    # link of the third node taken out.
    page.get_by_role('tab', name='Endpoint', exact=True).click()
    page.wait_for_function('cy.nodes().length > 3')
    placed = page.evaluate('cy.nodes()[0].position()')
    topology = json.loads(frozen)
    owner = {v['id']: v['owner'] for v in topology['vertices']}
    topology['edges'] = [e for e in topology['edges']
                         if 3 not in (owner.get(e['source']), owner.get(e['target']))]
    cut = json.dumps(topology)
    page.unroute('**/api/topology')
    page.route('**/api/topology', lambda route: route.fulfill(
        status=200, content_type='application/json', body=cut))
    page.wait_for_function('t.edges.length === %d' % len(topology['edges']), timeout=20000)
    assert page.evaluate('cy.nodes()[0].position()') == placed, 'the update moved a vertex'
    page.get_by_role('tab', name='Matrix', exact=True).click()
    unreachable = page.locator('td.no-path')
    assert unreachable.count() >= 2, unreachable.count()
    assert 'no route' in (unreachable.first.get_attribute('title') or '')
    phase('update without a route')
    page.get_by_role('tab', name='Endpoint', exact=True).click()
    page.unroute('**/api/topology')
    # A control API that stops answering is reported, and recovery is silent.
    page.route('**/api/topology', lambda route: route.abort())
    page.wait_for_function('$("connection").classList.contains("error")', timeout=15000)
    assert page.evaluate('$("connection").textContent') == 'Control API offline'
    page.unroute('**/api/topology')
    # A refusal from the API reads the same as silence, and the page recovers
    # from either on its own.
    page.route('**/api/topology', lambda route: route.fulfill(status=503, body='down'))
    page.wait_for_function('$("connection").title.includes("503")', timeout=20000)
    page.unroute('**/api/topology')
    page.wait_for_function('!$("connection").classList.contains("error")', timeout=20000)
    phase('control API loss and recovery')
    page.set_viewport_size({'width': 390, 'height': 844})
    for name in ['Hosts', 'Endpoint', 'Matrix', 'Configs']:
        page.get_by_role('tab', name=name, exact=True).click()
    assert not errors, errors
    phase('narrow viewport')
    if report := os.environ.get('MESH_TEST_WEB_COVERAGE'):
        coverage(session, report)
        phase('coverage written')
    browser.close()
