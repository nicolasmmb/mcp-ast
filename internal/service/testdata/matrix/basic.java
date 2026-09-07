import java.util.List;

public class Greeter {
  private int count = 0;

  public Greeter() {}

  public String hello(String who) {
    int local = 1;
    return "hi " + who + local;
  }

  public void run() {
    hello("world");
  }
}

interface Named {
  String name();
}

enum Color { RED, BLUE }

record Point(int x, int y) {}
