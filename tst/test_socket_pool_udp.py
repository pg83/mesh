"""UDP bind rejects occupied ports and closed ports spend one minute in quarantine."""
import lib
import ports


def test():
    ports.quarantine(lib.Lab(['a', 'b'], {1: ['a', 'b']}), 'udp')


lib.main(test)
