"""Small application probes, executed inside a node's namespace."""

import json
import os
import socket
import sys


def main():
    mode = sys.argv[1]
    if mode == 'stream':
        print(json.dumps({'pid': os.getpid(), 'connection': os.environ['SSH_CONNECTION']}), flush=True)
        for line in sys.stdin:
            print(line.rstrip('\n'), flush=True)
    elif mode == 'udp-server':
        sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        sock.bind((sys.argv[2], int(sys.argv[3])))
        print('ready', flush=True)
        while True:
            data, address = sock.recvfrom(65535)
            print(data.hex(), flush=True)
            sock.sendto(data, address)
    elif mode == 'udp-client':
        sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        sock.connect((sys.argv[2], int(sys.argv[3])))
        for line in sys.stdin:
            request = json.loads(line)
            if 'send' in request:
                sock.send(bytes.fromhex(request['send']))
                result = True
            else:
                sock.settimeout(request['recv'])
                try:
                    result = sock.recv(65535).hex()
                except socket.timeout:
                    result = None
            print(json.dumps(result), flush=True)
    else:
        raise ValueError(mode)


if __name__ == '__main__':
    main()
