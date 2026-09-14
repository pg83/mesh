"""WebSocket TCP sockets use the same source-port quarantine as UDP."""
import lib
import ports
import ws


def test():
    lab = ws.Lab(statics=['b'])
    lab.configs['a'] = dict(endpoint=[])
    ports.quarantine(lab, 'tcp')


lib.main(test)
