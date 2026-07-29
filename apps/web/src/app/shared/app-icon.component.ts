import { ChangeDetectionStrategy, Component } from '@angular/core';

@Component({
  selector: 'app-icon',
  changeDetection: ChangeDetectionStrategy.OnPush,
  host: {
    'aria-hidden': 'true',
    class: 'material-symbols-rounded',
  },
  template: '<ng-content />',
  styles: `
    :host {
      display: inline-flex;
      width: 1em;
      height: 1em;
      flex: none;
      align-items: center;
      justify-content: center;
      overflow: hidden;
      font-family: 'Material Symbols Rounded';
      font-size: 1.5rem;
      font-style: normal;
      font-weight: normal;
      line-height: 1;
      letter-spacing: normal;
      text-transform: none;
      white-space: nowrap;
      word-wrap: normal;
      direction: ltr;
      font-feature-settings: 'liga';
      font-variation-settings:
        'FILL' 0,
        'wght' 400,
        'GRAD' -25,
        'opsz' 24;
    }
  `,
})
export class AppIconComponent {}
