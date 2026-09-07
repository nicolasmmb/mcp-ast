using System;

namespace Demo {
  interface INamed {
    string Name { get; }
  }

  struct Point {
    public int X;
  }

  enum Color { Red, Blue }

  class Greeter {
    private int count = 0;
    public string Label { get; set; }

    public Greeter() {}

    public int Helper() {
      int local = 1;
      return local;
    }

    public void Main() {
      Helper();
      Helper();
    }
  }
}
