# pdfua-demo

A PDF/UA document produced by [glu](https://boxesandglue.dev) (the
boxesandglue typesetter), originally part of the speedata Marketing
material `glu-strategie/pdfua-demo.pdf`. Single-page, tagged via
Markdown front matter.

Covers in one fixture:

- Classical xref
- `/StructTreeRoot` with H1/H2/P/L/LI/LBody/BlockQuote/Code/Figure tags
- `/RoleMap` (none — the doc uses standard structure types)
- `/MarkInfo`, `/Lang`, `/ViewerPreferences`
- `/Metadata` XMP stream
- `/ParentTree` with mixed `Nums` array (int + ref + int + array of refs)
- Type0 / CIDFont / ToUnicode CMap fonts
- FlateDecode content stream with PDF/UA marked-content operators
  (`/H1 <</MCID 0>> BDC` etc.)
- `ActualText` entries in PDFDocEncoding with the en-dash (`0x85`)
- A binary file ID (`/ID` array)

This is what we want our parser to be able to read losslessly.
