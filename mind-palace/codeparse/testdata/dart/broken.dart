import 'package:flutter/widgets.dart';

/// Still found: the class before the error.
class Greeting extends StatelessWidget {
  const Greeting({super.key});

  @override
  Widget build(BuildContext context) => const Text('hello');
}

/// The error is inside half's body: the operand after + is missing.
class Broken {
  void half() {
    final total = 1 + ;
    show(total);
  }

  /// Still found: the getter after the error.
  int get value => 42;
}

/// Still found: the function after the error.
int answer() => compute(6, 7);
