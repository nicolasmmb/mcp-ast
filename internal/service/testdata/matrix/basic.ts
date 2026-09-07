import { readFile } from "fs";

interface Named {
  name: string;
}

type ID = string;

enum Color {
  Red,
  Blue,
}

class Greeter {
  hello(who: string): string {
    return "hi " + who;
  }
}

const counter = 0;

function helper(): number {
  return 1;
}

function main(): void {
  helper();
  helper();
}
