"""Version vectors stop gossip a peer already holds, even over a one-way channel, and resume it on change."""
import time
import lib


def versions(lab, name):
    return {r['owner']: r['version'] for r in lab.status(name)['records']}


def held(lab, name, owner):
    """The vector of `owner` as `name` last saw it."""
    return next((v['records'] for v in lab.status(name)['vectors'] if v['owner'] == owner), None)


def quiet(lab, rule, seconds=4):
    """No graph record passed the observed link for `seconds`."""
    before = rule['hits']
    time.sleep(seconds)
    return rule['hits'] == before


def test():
    with lib.Lab(['a', 'b', 'c'], {1: ['a', 'b', 'c']}) as lab:
        # b never sends anything to a; b's vector reaches a through c.
        lab.intercept('b', 'a', 'drop', count=-1)
        ab = lab.intercept('a', 'b', 'observe', kind=1, count=-1)
        ac = lab.intercept('a', 'c', 'observe', kind=1, count=-1)
        lab.wait_ping('a', 'c')
        lab.wait_ping('c', 'b')
        lab.wait(lambda: versions(lab, 'b').get(1) == versions(lab, 'a').get(1), 'a record reaches b over the one-way channel')
        lab.wait(lambda: (held(lab, 'a', 2) or {}).get('1') == versions(lab, 'a').get(1), 'b vector reaches a through c')
        lab.wait(lambda: quiet(lab, ab), 'no records to b once its vector confirms them', timeout=40)
        assert quiet(lab, ac), 'records still sent to c'
        assert any(l['from']['addr'] == '10.1.0.1' for l in lab.status('b')['links']), 'bundles keep the one-way link alive'
        # A change resumes gossip to b until b's vector catches up.
        before = ab['hits']
        lab.stop_node('c')
        lab.start_node('c')
        lab.wait_ping('a', 'c')
        lab.wait(lambda: versions(lab, 'b').get(1) == versions(lab, 'a').get(1) and versions(lab, 'b').get(3) == versions(lab, 'a').get(3),
                 'new versions reach b')
        lab.wait(lambda: quiet(lab, ab), 'gossip to b stops again', timeout=40)
        assert 0 < ab['hits'] - before < 40, ab['hits'] - before
        # Without any path back, a keeps sending to b every second: the safe fallback.
        lab.intercept('b', 'c', 'drop', count=-1)
        lab.stop_node('c')
        lab.start_node('c')
        lab.wait_ping('a', 'c')
        before = ab['hits']
        time.sleep(4)
        assert ab['hits'] - before >= 3, ab['hits'] - before
        assert versions(lab, 'b').get(3) == versions(lab, 'a').get(3), 'b still converges'


lib.main(test)
