<?php

namespace App\Http\Controllers;

use App\Models\User;
use App\Models\Post as BlogPost;
use App\Services\{Mailer, Logger as Log};
use Illuminate\Support\Facades\DB, Illuminate\Support\Str;
use function App\Support\helper;
use function App\Support\{format_date, format_money as money};
use const App\Support\VERSION;
use const App\Support\{LIMIT, MODE as APP_MODE};

require_once __DIR__ . '/../bootstrap.php';
require 'config/app.php';
include 'partials/header.php';
include_once dirname(__DIR__) . '/helpers.php';

class UserController
{
    public function show(int $id): User
    {
        Log::info('showing user', ['id' => $id]);
        $user = User::findOrFail($id);
        Mailer::send($user, money(10));
        return $user;
    }
}
