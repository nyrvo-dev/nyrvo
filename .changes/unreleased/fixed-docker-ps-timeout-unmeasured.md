- A `docker ps` probe that ran out of time is marked unmeasured instead of
  leaving an empty service list that looked like no containers were
  running. Cancelling the capture still aborts.
