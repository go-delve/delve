
Note:
-----

cfile-linux-amd64.syso was generated from the C source file that appears below.

Build with Clang version 10:

 $ clang-10 -O -gdwarf-5 -c cfile.c -o cfile.syso

The DWARF of interest is for the function "qtop". Triggering the bug
requires that the source file in question appears as the first item
in the DWARF line table file section, e.g.

$ llvm-dwarfdump-10 --debug-line cfile.syso
....
standard_opcode_lengths[DW_LNS_set_epilogue_begin] = 0
standard_opcode_lengths[DW_LNS_set_isa] = 1
include_directories[  0] = "/ssd2/go1/src/tmp/dlvbug"
file_names[  0]:
           name: "cfile.c"
      dir_index: 0
   md5_checksum: ...


// ------------------------begin source code for cfile.c----------------

#include <stdlib.h>
#include <string.h>

int glob = 99;

inline int qleaf(int lx, int ly, int *lv)
{
  lv[lx&3] += 3;
  return lv[ly&3];
}

int qmid(int mx, int my, int *lv, int *mv)
{
  mv[mx&3] += qleaf(mx, my, lv);
  return mv[my&3];
}

int qtop(int mx, int my)
{
  int mv[64], lv[66], n = (mx < 64 ? 64 : mx);

  memset(&mv[0], 9, sizeof(mv));
  memset(&lv[0], 11, sizeof(mv));
  return qmid(mx, my, lv, mv) + qleaf(mx, my, lv);
}

void Cfunc(int x) {
  glob += qtop(x, 43);
}

