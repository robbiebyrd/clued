import 'package:flutter/material.dart';

import 'counter_model.dart';

void main() {
  runApp(const CounterApp());
}

/// The app: a Material shell around the counter page.
class CounterApp extends StatelessWidget {
  const CounterApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'Counter',
      theme: ThemeData(
        colorScheme: ColorScheme.fromSeed(seedColor: Colors.deepPurple),
        useMaterial3: true,
      ),
      home: const CounterPage(title: 'Home'),
    );
  }
}

/// The page that counts taps.
class CounterPage extends StatefulWidget {
  const CounterPage({super.key, required this.title});

  /// The title in the app bar.
  final String title;

  @override
  State<CounterPage> createState() => _CounterPageState();
}

class _CounterPageState extends State<CounterPage>
    with SingleTickerProviderStateMixin
    implements CounterListener {
  final CounterModel _model = CounterModel();
  late final AnimationController _pulse;

  @override
  void initState() {
    super.initState();
    _pulse = AnimationController(vsync: this, duration: Durations.short4)
      ..addListener(_onTick)
      ..forward();
    _model.addListener(this);
  }

  @override
  void dispose() {
    _pulse.dispose();
    super.dispose();
  }

  void _onTick() => setState(() {});

  @override
  void onChanged(int value) {
    if (value % 10 == 0) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Reached $value')),
      );
    }
  }

  void _increment() {
    setState(() {
      _model.increment();
    });
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Scaffold(
      appBar: AppBar(
        backgroundColor: theme.colorScheme.inversePrimary,
        title: Text(widget.title),
      ),
      body: Center(
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: <Widget>[
            const Text('You have pushed the button this many times:'),
            ScaleTransition(
              scale: _pulse,
              child: Text(
                '${_model.count}',
                style: theme.textTheme.headlineMedium,
              ),
            ),
          ],
        ),
      ),
      floatingActionButton: FloatingActionButton(
        onPressed: _increment,
        tooltip: 'Increment',
        child: const Icon(Icons.add),
      ),
    );
  }
}
