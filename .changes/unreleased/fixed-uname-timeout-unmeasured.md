- A `uname` probe that ran out of time is recorded as unmeasured rather
  than as a machine with no kernel string. A missing uname binary still
  leaves the kernel field empty.
