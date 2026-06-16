import { Component, OnInit, inject, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatChipsModule } from '@angular/material/chips';
import { MatDividerModule } from '@angular/material/divider';
import { MatIconModule } from '@angular/material/icon';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatToolbarModule } from '@angular/material/toolbar';
import { RouterModule } from '@angular/router';
import { MAT_NAVIGATION_SUITE_MODULES } from '@fairylights-studio/ngx-m3-navigation-suite';

@Component({
  imports: [MAT_NAVIGATION_SUITE_MODULES,
    MatButtonModule,
    MatCardModule,
    MatChipsModule,
    MatDividerModule,
    MatIconModule,
    MatProgressBarModule,
    MatToolbarModule,
    RouterModule,
  ],
  selector: 'app-root',
  templateUrl: './app.html',
})
export class App  {
  selectedIndex = 0;

}
