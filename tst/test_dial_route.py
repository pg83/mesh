"""A node dials a target only from the interface whose network holds it or through which the system routes it."""
import time
import lib


class Lab(lib.Lab):
    def registry(self):
        registry = super().registry()
        # b also advertises an address on a network a has no route to.
        registry[1]['endpoint'] += [dict(proto='udp', addr='10.9.0.5', port=7000),
                                    dict(proto='ws', addr='localhost', port=7100, path='/mesh')]
        return registry


def test():
    with Lab(['a', 'b'], {1: ['a', 'b'], 2: ['a']}, statics=['b']) as lab:
        lab.wait_ping('a', 'b')
        lab.wait_ping('b', 'a')

        def sources(target):
            """Addresses of a's outgoing channel sources towards the listener at `target`."""
            status = lab.status('a')
            described = lambda vertex: status['addresses'].get(str(vertex), {})
            return sorted({described(c['from']).get('addr') for c in status['channels']
                           if c['outgoing'] and described(c['to']).get('addr') == target})

        time.sleep(3)
        # The second segment's address cannot reach b and never dials it.
        assert sources('10.1.0.2') == ['10.1.0.1'], sources('10.1.0.2')
        assert sources('10.9.0.5') == [], sources('10.9.0.5')
        assert sources('localhost') == [], sources('localhost')
        # A route makes the target reachable from the interface it goes through, and only that one.
        lab.run('a', ['ip', 'route', 'add', '10.9.0.0/24', 'via', '10.1.0.2'])
        lab.wait(lambda: sources('10.9.0.5') == ['10.1.0.1'], 'routed target dialed from the routing interface', timeout=45)
        assert sources('10.1.0.2') == ['10.1.0.1']


lib.main(test)
