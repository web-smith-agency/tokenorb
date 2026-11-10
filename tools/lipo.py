#!/usr/bin/env python3
"""Mini-lipo: baut aus arm64 + x86_64 Mach-O eine Universal-Datei."""
import struct, sys
out, *ins = sys.argv[1:]
CPU = {0x0100000C: (0x0100000C, 0), 0x01000007: (0x01000007, 3)}
slices = []
for p in ins:
    data = open(p, 'rb').read()
    magic, cputype, cpusub = struct.unpack('<III', data[:12])
    assert magic == 0xFEEDFACF, p
    slices.append((cputype, cpusub, data))
ALIGN = 14
off = 1 << ALIGN
hdr = struct.pack('>II', 0xCAFEBABE, len(slices))
body = b''
entries = []
pos = off
for ct, cs, data in slices:
    entries.append(struct.pack('>iiIII', ct, cs, pos, len(data), ALIGN))
    pad = (-(pos + len(data))) % (1 << ALIGN)
    body += data + b'\0' * pad
    pos += len(data) + pad
head = hdr + b''.join(entries)
open(out, 'wb').write(head + b'\0' * (off - len(head)) + body)
print("universal:", out, [hex(s[0]) for s in slices])
