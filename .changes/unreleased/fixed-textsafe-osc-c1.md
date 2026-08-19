- Terminal sanitization now consumes OSC sequences and 8-bit C1
  introducers, not only CSI, so a loaded snapshot cannot retain an escape
  sequence in fields that reach the terminal.
