Fixed textsafe stripping DCS, APC, and SOS terminal sequences in full.
 SOS is consumed too, in both its 7-bit (ESC X) and C1 (U+0098) forms: the case list carried 'p', which is not an introducer at all, so an SOS payload passed through as text a terminal would still act on.
