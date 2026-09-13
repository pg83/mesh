"""Chromium exercises the live web UI in the observer's network namespace."""
import json
import os
from pathlib import Path
from playwright.sync_api import sync_playwright

with sync_playwright() as p:
    browser = p.chromium.launch(args=['--no-sandbox'])
    page = browser.new_page(viewport={'width': 1440, 'height': 900}, accept_downloads=True)
    errors = []
    page.on('pageerror', lambda error: errors.append(str(error)))
    page.goto('http://127.0.0.1:8059/')
    page.wait_for_function('ready && t.peers.length === 3')
    assert page.get_by_role('tab').count() == 4
    assert page.evaluate('cy.nodes().length') >= 6
    assert page.evaluate('getComputedStyle(document.querySelector(".graph-panel")).borderWidth') == '0px'
    assert page.evaluate('getComputedStyle(document.querySelector(".inspector")).borderWidth') == '0px'
    assert page.evaluate('getComputedStyle(document.querySelector("main")).padding') == '0px'
    # Dragging/zooming survives periodic refreshes.
    page.evaluate('cy.zoom(1.3); cy.pan({x: 71, y: 83}); cy.nodes()[0].position({x:321,y:123})')
    page.wait_for_timeout(3500)
    assert page.evaluate('cy.zoom()') == 1.3
    assert page.evaluate('cy.pan()') == {'x': 71, 'y': 83}
    assert page.evaluate('cy.nodes()[0].position()') == {'x': 321, 'y': 123}
    page.get_by_role('tab', name='Хосты', exact=True).click()
    assert page.evaluate('cy.nodes().length') == 3
    page.get_by_role('tab', name='Matrix', exact=True).click()
    page.wait_for_selector('#matrix-page:not([hidden])')
    hop = page.get_by_role('button', name='2', exact=True).first
    assert 'a → work' in (hop.get_attribute('title') or '')
    hop.click()
    assert page.get_by_role('tab', name='Endpoint', exact=True).get_attribute('aria-selected') == 'true'
    assert page.evaluate('cy.edges(".focus").length') >= 2
    page.get_by_role('tab', name='Конфиги', exact=True).click()
    with page.expect_download() as download:
        page.get_by_role('link', name='Скачать mesh-2.json ↓').click()
    config = json.loads(Path(download.value.path()).read_text())
    assert config['index'] == 2 and 'key' not in config and len(config['registry']) == 3
    page.goto('http://127.0.0.1:8059/config')
    page.wait_for_selector('#config-cards .config-card')
    assert page.get_by_role('tab', name='Конфиги', exact=True).get_attribute('aria-selected') == 'true'
    page.get_by_role('tab', name='Endpoint', exact=True).click()
    page.wait_for_function('cy.nodes().length > 3')
    if artifacts := os.environ.get('MESH_TEST_ARTIFACTS'):
        Path(artifacts).mkdir(parents=True, exist_ok=True)
        page.screenshot(path=str(Path(artifacts) / 'mesh-web.png'))
    page.set_viewport_size({'width': 390, 'height': 844})
    for name in ['Хосты', 'Endpoint', 'Matrix', 'Конфиги']:
        page.get_by_role('tab', name=name, exact=True).click()
    assert not errors, errors
    browser.close()
