# Firmware

- `lib/KickerModule/` – Arduino library implementing the module protocol (spec 01). Works on any board with
  an Arduino core and USB serial; reference boards are the RP2040-Zero and RP2350 (D13).
- `modules/<name>/` – one PlatformIO project per reference module.

```sh
pio run -d modules/goal-sensor                 # build
pio run -d modules/goal-sensor -t upload       # flash (hold BOOT while plugging in the first time)
pio device monitor -d modules/goal-sensor      # raw serial monitor
```

To talk to a module like the server would, without a Pi: `kicker monitor <port>` (M1, task 4).
