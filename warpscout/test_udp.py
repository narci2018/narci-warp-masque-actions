import socket
s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
s.connect(("127.0.0.1", 29882))
s.sendall(b"\x05\x01\x00")
res = s.recv(2)
print("Handshake:", res.hex())
s.sendall(b"\x05\x03\x00\x01\x00\x00\x00\x00\x00\x00")
res2 = s.recv(10)
print("UDP Associate Resp:", res2.hex())
