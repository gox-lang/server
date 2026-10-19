#!/usr/bin/env python3
"""Check a packaged GOX executable using its real stdio LSP transport."""
import json
import subprocess
import sys
from pathlib import Path

binary = str(Path(sys.argv[1]).resolve())
subprocess.run([binary, "version"], check=True, timeout=30)
messages = [
    {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {}},
    {"jsonrpc": "2.0", "id": 2, "method": "shutdown"},
    {"jsonrpc": "2.0", "method": "exit"},
]
wire = b""
for message in messages:
    body = json.dumps(message).encode()
    wire += f"Content-Length: {len(body)}\r\n\r\n".encode() + body
result = subprocess.run([binary, "ls"], input=wire, capture_output=True, timeout=300, check=True)
remaining = result.stdout
responses = {}
while remaining:
    header, remaining = remaining.split(b"\r\n\r\n", 1)
    fields = dict(line.split(b":", 1) for line in header.split(b"\r\n"))
    length = int(fields[b"Content-Length"].strip())
    body, remaining = remaining[:length], remaining[length:]
    message = json.loads(body)
    if "id" in message:
        responses[message["id"]] = message
assert "capabilities" in responses[1]["result"], responses
assert responses[2]["result"] is None, responses
assert not any("error" in response for response in responses.values()), responses
print("GOX version and LSP initialize/shutdown passed")
